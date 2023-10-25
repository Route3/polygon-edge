package txfromcsv

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"github.com/0xPolygon/polygon-edge/network"
	"github.com/0xPolygon/polygon-edge/network/grpc"
	"github.com/0xPolygon/polygon-edge/txfromcsv/proto"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/types/known/emptypb"
	"os"
	"strings"
	"sync"
	"time"
)

type TxPoolInterface interface {
	Prepare()
	Length() uint64
	Peek() *types.Transaction
	Pop(tx *types.Transaction)
	Drop(tx *types.Transaction)
	Demote(tx *types.Transaction)
	ResetWithHeaders(headers ...*types.Header)
	SetSealing(bool)
	Init() error
}

type txfromcsv struct {
	proto.UnimplementedTxOpsServer

	ctx             context.Context
	log             hclog.Logger
	txs             []*types.Transaction
	txCsvFile       string
	totalValidators int

	stream  *grpc.GrpcStream
	network *network.Server
	clients []proto.TxOpsClient

	mux sync.Mutex
	wg  sync.WaitGroup
}

const txFromCsv = "/txfromcsv/0.1"

func NewTxFromCsv(log hclog.Logger, network *network.Server, csvFile string, validatorNumber int) (TxPoolInterface, error) {
	t := &txfromcsv{
		ctx:             context.Background(),
		log:             log.Named("txfromcsv"),
		txCsvFile:       csvFile,
		totalValidators: validatorNumber,

		mux:     sync.Mutex{},
		wg:      sync.WaitGroup{},
		network: network,
	}

	if err := t.getTxsFromCsv(); err != nil {
		t.log.Error("Could not get txs from csv", "err", err)
		os.Exit(1)
	}

	t.stream = grpc.NewGrpcStream()
	proto.RegisterTxOpsServer(t.stream, t)
	t.stream.Serve()
	t.network.RegisterProtocol(txFromCsv, t.stream)

	//config := chain.AllForksEnabled.At(0)
	//signer := crypto.NewSigner(config, uint64(62855261))
	//
	//f, err := os.Create("tx_dump.csv")
	//if err != nil {
	//	fmt.Println(err)
	//	os.Exit(1)
	//}
	//
	//c := csv.NewWriter(f)
	//c.Write([]string{
	//	"From",
	//	"To",
	//	"Nonce",
	//	"GasPrice",
	//	"Gas",
	//	"Value",
	//	"Hash",
	//})
	//
	//var to string
	//
	//for _, tx := range t.txs {
	//	from, _ := signer.Sender(tx)
	//	if tx.To != nil {
	//		to = tx.To.String()
	//	}
	//
	//	c.Write([]string{
	//		from.String(),
	//		to,
	//		fmt.Sprintf("%d", tx.Nonce),
	//		fmt.Sprintf("%d", tx.GasPrice),
	//		fmt.Sprintf("%d", tx.Gas),
	//		fmt.Sprintf("%s", tx.Value.String()),
	//		fmt.Sprintf("%s", tx.Hash.String()),
	//	})
	//
	//}
	//
	//c.Flush()
	//
	//os.Exit(1)

	return t, nil
}

func (t *txfromcsv) Init() error {
	timeOut := time.After(time.Minute * 2)
	ticker := time.Tick(time.Second * 5)

	// block until there are peers found
	for {
		select {
		case <-timeOut:
			t.log.Error("TxfromCsv initialization timeout")
			return fmt.Errorf("txfromcsv init timeout")
		case <-ticker:
			// Total number of nodes -1 as we don't count ourselves
			totalPeers := t.totalValidators - 1
			peers := t.network.Peers()

			if len(peers) != totalPeers {
				t.log.Debug("Total peer number less than expected", "expected", totalPeers)
				continue
			}

			for _, p := range peers {
				conn, err := t.network.NewProtoConnection(txFromCsv, p.Info.ID)
				if err != nil {
					t.log.Error("Could not create grpc conn to client", "id", p.Info.ID.String())
					continue
				}

				t.network.SaveProtocolStream(txFromCsv, conn, p.Info.ID)

				t.clients = append(t.clients, proto.NewTxOpsClient(conn))
			}

			t.log.Info("Successful grpc to peers", "conn", txFromCsv, "total_peers", totalPeers)

			return nil
		}
	}

}

func (t *txfromcsv) Prepare() {}

func (t *txfromcsv) Length() uint64 {
	return uint64(len(t.txs))
}

func (t *txfromcsv) Peek() *types.Transaction {
	if len(t.txs) == 0 {
		return nil
	}

	t.log.Debug("Calling peek", "total_tx_left", len(t.txs))

	return t.txs[0]
}

func (t *txfromcsv) Pop(tx *types.Transaction) {
	if len(t.txs) == 0 {
		return
	}

	t.log.Debug("Popping transaction", "hash", tx.Hash.String())

	t.mux.Lock()
	t.txs = t.txs[1:]
	t.mux.Unlock()

	for _, cl := range t.clients {
		_, err := cl.TxPop(t.ctx, &proto.TxData{TxHash: tx.Hash.String()})
		if err != nil {
			t.log.Error("Could not send grpc pop transaction", "err", err, "hash", tx.Hash.String())
			return
		}
	}
}

func (t *txfromcsv) Drop(tx *types.Transaction) {
	if len(t.txs) == 0 {
		return
	}

	t.log.Debug("Dropping transaction", "hash", tx.Hash.String())

	t.mux.Lock()
	t.txs = t.txs[1:]
	t.mux.Unlock()

	for _, cl := range t.clients {
		t.wg.Add(1)
		cl := cl

		go func() {
			defer t.wg.Done()

			_, err := cl.TxPop(t.ctx, &proto.TxData{TxHash: tx.Hash.String()})
			if err != nil {
				t.log.Error("Could not send grpc pop transaction", "err", err, "hash", tx.Hash.String())
				return
			}
		}()
	}

	t.wg.Wait()

}

func (t *txfromcsv) Demote(_ *types.Transaction) {
}

func (t *txfromcsv) ResetWithHeaders(_ ...*types.Header) {}

func (t *txfromcsv) SetSealing(_ bool) {}

func (t *txfromcsv) TxPop(_ context.Context, data *proto.TxData) (*emptypb.Empty, error) {
	if len(t.txs) == 0 {
		return new(emptypb.Empty), nil
	}

	txToPop := t.txs[0]
	if txToPop.Hash.String() != data.GetTxHash() {
		t.log.Error("Tx hashes dont match", "have", txToPop.Hash.String(), "got", data.GetTxHash())
		return new(emptypb.Empty), fmt.Errorf(
			fmt.Sprintf("tx hashes dont match, have: %s got: %s", txToPop.Hash.String(), data.GetTxHash()),
		)
	}

	t.mux.Lock()
	t.txs = t.txs[1:]
	t.mux.Unlock()
	t.log.Info("Transaction removed", "hash", data.GetTxHash())

	return new(emptypb.Empty), nil
}

func (t *txfromcsv) getTxsFromCsv() error {
	f, err := os.Open("tx.csv")
	if err != nil {
		return fmt.Errorf("unable to read input file: %w", err)
	}
	defer f.Close()

	csvReader := csv.NewReader(f)
	c, err := csvReader.ReadAll()
	if err != nil {
		return err
	}

	for _, trn := range c {
		tx := &types.Transaction{}
		if err := tx.UnmarshalRLP(stringToBytes(trn[0])); err != nil {
			t.log.Error("Could not unmarshalRLP: %w", err)
		}

		t.txs = append(t.txs, tx)
	}

	return nil
}

func stringToBytes(str string) []byte {
	str = strings.TrimPrefix(str, "0x")
	if len(str)%2 == 1 {
		str = "0" + str
	}

	b, _ := hex.DecodeString(str)

	return b
}
