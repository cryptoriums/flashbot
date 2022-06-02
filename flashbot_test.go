// Copyright (c) The Cryptorium Authors.
// Licensed under the MIT License.

package flashbot

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cryptoriums/packages/env"
	"github.com/cryptoriums/packages/testutil"
	tx_p "github.com/cryptoriums/packages/tx"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
)

const (
	gasLimit    = 3_000_000
	gasPrice    = 10
	blockNumMax = 10

	// Some ERC20 token with approve function.
	contractAddressGoerli  = "0xf74a5ca65e4552cff0f13b116113ccb493c580c5"
	contractAddressMainnet = "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"
)

var logger = log.With(
	log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)),
	"ts", log.TimestampFormat(func() time.Time { return time.Now().UTC() }, "jan 02 15:04:05.00"),
	"caller", log.Caller(4),
)

func TestExample(t *testing.T) {
	ctx := context.Background()

	envr, err := env.LoadFromEnvVarOrFile("env", "env.json")
	testutil.Ok(t, err)

	client, err := ethclient.DialContext(ctx, envr.Nodes[0].URL)
	testutil.Ok(t, err)

	netID, err := client.NetworkID(ctx)
	testutil.Ok(t, err)
	level.Info(logger).Log("msg", "network", "id", netID.String(), "node", envr.Nodes[0].URL)

	privKey, pubKey, err := Keys(envr.Accounts[0].Priv)
	testutil.Ok(t, err)

	level.Info(logger).Log("msg", "pub key for", "addr", pubKey.Hex())

	endpoint, err := DefaultApi(netID.Int64())
	testutil.Ok(t, err)

	flashbot, err := New(privKey, endpoint)
	testutil.Ok(t, err)

	nonce, err := client.NonceAt(ctx, *pubKey, nil)
	testutil.Ok(t, err)

	addr, err := GetContractAddress(netID)
	testutil.Ok(t, err)

	// // Make a call to estimate gas.
	// {
	// 	blockNumber, err := client.BlockNumber(ctx)
	// 	testutil.Ok(t,err)
	// 	resp, err := flashbot.EstimateGasBundle(
	// 		ctx,
	// 		[]Tx{
	// 			{
	// 				From: *pubKey,
	// 				To:   common.HexToAddress(contractAddressMainnet),
	// 				Data: data,
	// 			},
	// 		},
	// 		blockNumber,
	// 	)
	// 	testutil.Ok(t,err)

	// 	level.Info(logger).Log("msg", "Called Bundle",
	// 		"respStruct", fmt.Sprintf("%+v", resp),
	// 	)
	// }

	tx, txHex, err := tx_p.NewSignedTX(
		ctx,
		privKey,
		addr,
		ContractABI,
		nonce,
		netID.Int64(),
		"approve",
		// Use random address so that the TX uses more than the required 42k gas.
		[]interface{}{randomAddress(), big.NewInt(1)},
		gasLimit,
		gasPrice,
		gasPrice,
		0,
	)
	testutil.Ok(t, err)
	level.Info(logger).Log("msg", "created transaction", "hash", tx.Hash())

	// Make a request to the Call endpoint for simulation.
	{
		resp, err := flashbot.CallBundle(
			ctx,
			[]string{txHex},
			0,
		)
		testutil.Ok(t, err)

		level.Info(logger).Log("msg", "Called Bundle",
			"respStruct", fmt.Sprintf("%+v", resp),
		)
	}

	// Make a call to the Send endpoint.
	{
		blockNumber, err := client.BlockNumber(ctx)
		testutil.Ok(t, err)

		level.Info(logger).Log("msg", "created send transaction", "hash", tx.Hash())

		var resp *Response
		for i := uint64(1); i < blockNumMax; i++ {
			resp, err = flashbot.SendBundle(
				ctx,
				[]string{txHex},
				blockNumber+i,
			)
			time.Sleep(100 * time.Millisecond)
			testutil.Ok(t, err)
		}

		level.Info(logger).Log("msg", "Sent Bundle",
			"blockMax", strconv.Itoa(int(blockNumMax)),
			"respStruct", fmt.Sprintf("%+v", resp),
		)
	}

}

func ExitOnError(logger log.Logger, err error) {
	if err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}
}

func Keys(_privateKey string) (*ecdsa.PrivateKey, *common.Address, error) {
	privateKey, err := crypto.HexToECDSA(strings.TrimSpace(_privateKey))
	if err != nil {
		return nil, nil, err
	}

	publicKey := privateKey.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, nil, errors.New("casting public key to ECDSA")
	}

	publicAddress := crypto.PubkeyToAddress(*publicKeyECDSA)
	return privateKey, &publicAddress, nil
}

func GetContractAddress(networkID *big.Int) (common.Address, error) {
	switch netID := networkID.Int64(); netID {
	case 1:
		return common.HexToAddress(contractAddressMainnet), nil
	case 5:
		return common.HexToAddress(contractAddressGoerli), nil
	default:
		return common.Address{}, errors.Errorf("network id not supported id:%v", netID)
	}
}

func randomAddress() common.Address {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	return common.HexToAddress(hex.EncodeToString(bytes))
}

const ContractABI = `[
	{
	   "inputs":[
		  {
			 "internalType":"address",
			 "name":"spender",
			 "type":"address"
		  },
		  {
			 "internalType":"uint256",
			 "name":"value",
			 "type":"uint256"
		  }
	   ],
	   "name":"approve",
	   "outputs":[
		  {
			 "internalType":"bool",
			 "name":"",
			 "type":"bool"
		  }
	   ],
	   "stateMutability":"nonpayable",
	   "type":"function"
	}
 ]`
