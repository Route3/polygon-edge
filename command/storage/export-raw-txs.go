package storage

import (
	"fmt"
	"path/filepath"

	"github.com/0xPolygon/polygon-edge/blockchain/storage/leveldb"
	"github.com/hashicorp/go-hclog"
	"github.com/spf13/cobra"
)

var (
	dataDir string
)

func GetExportRawTxsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export-raw-txs",
		Short: "Explore the blockchain storage of Polygon Edge",
		Run:   runCommand,
	}

	cmd.Flags().StringVar(
		&dataDir,
		"data-dir",
		"data/",
		"the directory for the blockhain data",
	)

	return cmd
}

func runCommand(cmd *cobra.Command, args []string) {
	db, err := leveldb.NewLevelDBStorage(filepath.Join(dataDir), hclog.NewNullLogger())
	if err != nil {
		fmt.Printf("cannot open leveldb storage")
		return
	}

	head, _ := db.ReadHeadNumber()

	blockNumber := uint64(0)
	for blockNumber <= head {
		hash, ok := db.ReadCanonicalHash(blockNumber)

		if !ok {
			fmt.Printf("hash not found for block number %d", blockNumber)
			return
		}
		body, err := db.ReadBody(hash)

		if blockNumber != 0 && err != nil {
			fmt.Printf("error fetching body for block #%d %s\n", blockNumber, hash)
			return
		}

		if len(body.Transactions) > 0 {
			for _, tx := range body.Transactions {
				data := tx.MarshalRLPTo(nil)
				fmt.Printf("0x%x\n", data)
			}
		}

		blockNumber += 1
	}
}
