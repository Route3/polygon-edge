package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/0xPolygon/polygon-edge/e2e-polybft/framework"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestE2E_NetworkDiscoveryProtocol(t *testing.T) {
	const (
		validatorCount    = 5
		nonValidatorCount = 5
		// there is race condition which results that libp2p can execute ConnectedF twice and
		// both peers can close their outgoing/incoming connection
		// because of that, we are relaxing condition of 9 connections to 7 for each peer
		atLeastPeers = 7
		testTimeout  = time.Second * 60
	)

	// create cluster
	cluster := framework.NewTestCluster(t, 10,
		framework.WithValidatorSnapshot(validatorCount), framework.WithNonValidators(nonValidatorCount))
	defer cluster.Stop()

	ctx := context.Background()

	// wait for everyone to have at least 'atLeastPeers' peers
	err := cluster.WaitForGeneric(testTimeout, func(ts *framework.TestServer) bool {
		peerList, err := ts.Conn().PeersList(ctx, &emptypb.Empty{})

		return err == nil && len(peerList.GetPeers()) >= atLeastPeers
	})
	assert.NoError(t, err)
}