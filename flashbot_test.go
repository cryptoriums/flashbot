// Copyright (c) The Cryptorium Authors.
// Licensed under the MIT License.

package flashbot

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cryptoriums/telliot/pkg/private_file"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/params"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	"golang.org/x/tools/godoc/util"
)

const (
	gasLimit    = 3_000_000
	gasPrice    = 10 * params.GWei
	blockNumMax = 10

	// Some ERC20 token with approve function.
	contractAddressGoerli  = "0xf74a5ca65e4552cff0f13b116113ccb493c580c5"
	contractAddressMainnet = "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"
)

var logger log.Logger

func init() {
	logger = log.With(
		log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)),
		"ts", log.TimestampFormat(func() time.Time { return time.Now().UTC() }, "jan 02 15:04:05.00"),
		"caller", log.Caller(5),
	)

	env, err := ioutil.ReadFile(".env")
	if os.IsNotExist(err) { // In the CI the file doesn't exist and sets directly the env vars.
		return
	}
	ExitOnError(logger, err)
	if !util.IsText(env) {
		level.Info(logger).Log("msg", "env file is encrypted")
		env = private_file.DecryptWithPasswordLoop(env)
	}

	rr := bytes.NewReader(env)
	envMap, err := godotenv.Parse(rr)
	ExitOnError(logger, err)

	// Copied from the godotenv source code.
	currentEnv := map[string]bool{}
	rawEnv := os.Environ()
	for _, rawEnvLine := range rawEnv {
		key := strings.Split(rawEnvLine, "=")[0]
		currentEnv[key] = true
	}

	for key, value := range envMap {
		if !currentEnv[key] {
			os.Setenv(key, value)
		}
	}
}

type FlashboterCreator func(netID int64, prvKeyCall *ecdsa.PrivateKey, prvKeySend *ecdsa.PrivateKey, url string) (Flashboter, error)

func ExampleNewAll() {
	var newAll = func(netID int64, prvKeyCall *ecdsa.PrivateKey, prvKeySend *ecdsa.PrivateKey, url string) (Flashboter, error) {
		return NewAll(netID, prvKeyCall, prvKeySend)
	}
	run(newAll)
	// Output:
}

func Example() {
	run(New)
	// Output:
}

func run(flashbotCreator FlashboterCreator) {
	ctx := context.Background()

	nodeURL := os.Getenv("NODE_URL")

	client, err := ethclient.DialContext(ctx, nodeURL)
	ExitOnError(logger, err)

	netID, err := client.NetworkID(ctx)
	ExitOnError(logger, err)
	level.Info(logger).Log("msg", "network", "id", netID.String(), "node", nodeURL)

	privKeyC, pubKeyC, err := GetKey("ETH_PRIVATE_KEY_CALL")
	ExitOnError(logger, err)

	level.Info(logger).Log("msg", "pub key for call", "addr", pubKeyC.Hex())

	privKeyS, pubKeyS, err := GetKey("ETH_PRIVATE_KEY_SEND")
	ExitOnError(logger, err)

	level.Info(logger).Log("msg", "pub key for send", "addr", pubKeyC.Hex())

	flashbot, err := flashbotCreator(netID.Int64(), privKeyC, privKeyS, "")
	ExitOnError(logger, err)

	nonce, err := client.NonceAt(ctx, *pubKeyC, nil)
	ExitOnError(logger, err)

	abiP, err := abi.JSON(strings.NewReader(ContractABI))
	ExitOnError(logger, err)

	data, err := abiP.Pack(
		"approve",
		common.HexToAddress("0xd2ebc17f4dae9e512cae16da5ea9f55b7f65a623"),
		big.NewInt(1),
	)
	ExitOnError(logger, err)

	addr, err := GetContractAddress(netID)
	ExitOnError(logger, err)

	// Make a request to the Call endpoint for simulation.
	{

		txHex, tx, err := NewSignedTX(
			netID.Int64(),
			data,
			gasLimit,
			big.NewInt(gasPrice),
			big.NewInt(0),
			addr,
			nonce,
			privKeyC,
		)
		ExitOnError(logger, err)
		level.Info(logger).Log("msg", "created call transaction", "hash", tx.Hash())

		resp, err := flashbot.CallBundle(
			[]string{txHex},
			5*time.Second,
		)
		ExitOnError(logger, err)

		level.Info(logger).Log("msg", "Called Bundle",
			"respStruct", fmt.Sprintf("%+v", resp),
		)
	}

	// Make a call to the Send endpoint.
	{
		blockNumber, err := client.BlockNumber(ctx)
		ExitOnError(logger, err)
		nonce, err = client.NonceAt(ctx, *pubKeyS, nil)
		ExitOnError(logger, err)

		txHex, tx, err := NewSignedTX(
			netID.Int64(),
			data,
			gasLimit,
			big.NewInt(gasPrice),
			big.NewInt(0),
			addr,
			nonce,
			privKeyS,
		)
		ExitOnError(logger, err)
		level.Info(logger).Log("msg", "created send transaction", "hash", tx.Hash())

		var resp *Response
		for i := uint64(1); i < blockNumMax; i++ {
			resp, err = flashbot.SendBundle(
				[]string{txHex},
				blockNumber+i,
				5*time.Second,
			)
			ExitOnError(logger, err)
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

func GetKey(envName string) (*ecdsa.PrivateKey, *common.Address, error) {
	_privateKey := os.Getenv(envName)
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

func Keccak256(input []byte) [32]byte {
	hash := crypto.Keccak256(input)
	var hashed [32]byte
	copy(hashed[:], hash)

	return hashed
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
