package flashbot

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/log/level"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
)

const (
	gasLimit     = 3_000_000
	gasTip       = 2000000000 // 2 Gwei
	blockNumWait = 10

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
	err := godotenv.Load(".env")

	ExitOnError(logger, err)
}

func Example() {
	ctx, cncl := context.WithTimeout(context.Background(), time.Hour)
	defer cncl()

	nodeURL := os.Getenv("NODE_URL")

	client, err := ethclient.DialContext(ctx, nodeURL)
	ExitOnError(logger, err)

	block, err := client.HeaderByNumber(ctx, nil)
	ExitOnError(logger, err)
	baseFee := block.BaseFee

	netID, err := client.NetworkID(ctx)
	ExitOnError(logger, err)
	level.Info(logger).Log("msg", "network", "id", netID.String(), "node", nodeURL)

	addr, err := GetContractAddress(netID)
	ExitOnError(logger, err)

	pubKey, privKey, err := GetKeys()
	ExitOnError(logger, err)

	flashbot, err := New(netID.Int64(), privKey)
	ExitOnError(logger, err)

	// Prepare the data for the TX.
	nonce, err := client.NonceAt(ctx, *pubKey, nil)
	ExitOnError(logger, err)

	abiP, err := abi.JSON(strings.NewReader(ContractABI))
	ExitOnError(logger, err)

	data, err := abiP.Pack(
		"approve",
		common.HexToAddress("0xdf032bc4b9dc2782bb09352007d4c57b75160b15"),
		big.NewInt(1),
	)
	ExitOnError(logger, err)

	txHex, tx, err := flashbot.NewSignedTX(
		data,
		gasLimit,
		big.NewInt(baseFee.Int64()+baseFee.Int64()*126/1000),
		big.NewInt(gasTip),
		addr,
		nonce,
	)
	ExitOnError(logger, err)

	level.Info(logger).Log("msg", "created transaction", "hash", tx.Hash())

	blockNumber, err := client.BlockNumber(ctx)
	ExitOnError(logger, err)

	blockNumWaitMax := blockNumber + blockNumWait

	respCall, r, err := flashbot.CallBundle(
		[]string{txHex},
		blockNumWaitMax,
	)
	ExitOnError(logger, err)

	level.Info(logger).Log("msg", "Called Bundle",
		"resp", respCall,
		"blockMax", strconv.Itoa(int(blockNumWaitMax)),
		"respStruct", fmt.Sprintf("%+v", r),
	)

	var respSend string
	for i := uint64(0); i < 20; i++ {
		respSend, _, err = flashbot.SendBundle(
			[]string{txHex},
			blockNumWaitMax+i,
		)
		ExitOnError(logger, err)
	}

	level.Info(logger).Log("msg", "Sent Bundle",
		"resp", respSend,
		"blockMax", strconv.Itoa(int(blockNumWaitMax)),
		"respStruct", fmt.Sprintf("%+v", r),
	)

	// Output:
}

func ExitOnError(logger log.Logger, err error) {
	if err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}
}

func GetKeys() (*common.Address, *ecdsa.PrivateKey, error) {
	_privateKey := os.Getenv("ETH_PRIVATE_KEY")
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
	return &publicAddress, privateKey, nil
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
