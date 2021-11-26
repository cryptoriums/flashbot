// Copyright (c) The Cryptorium Authors.
// Licensed under the MIT License.

// Package flashbot provides a structured way to send TX to the flashbot relays.
package flashbot

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

type Params struct {
	Txs              []string `json:"txs,omitempty"`
	BlockNumber      string   `json:"blockNumber,omitempty"`
	StateBlockNumber string   `json:"stateBlockNumber,omitempty"`
	BundleHash       string   `json:"bundleHash,omitempty"`
}

type Request struct {
	Jsonrpc string   `json:"jsonrpc,omitempty"`
	Id      int      `json:"id,omitempty"`
	Method  string   `json:"method,omitempty"`
	Params  []Params `json:"params,omitempty"`
}

type Metadata struct {
	CoinbaseDiff      string
	EthSentToCoinbase string
	GasFees           string
}

type Result struct {
	BundleGasPrice string
	BundleHash     string
	Metadata
	Results []TxResult
}

type ResultBundleStats struct {
	Error
	Result BundleStats
}

type BundleStats struct {
	IsSimulated    bool
	IsHighPriority bool
	SimulatedAt    time.Time
	SubmittedAt    time.Time
	SentToMinersAt time.Time
}

type TxResult struct {
	Metadata
	FromAddress string
	GasPrice    string
	TxHash      string
	Error       string
	Revert      string
	GasUsed     uint64
}

type Error struct {
	Code    int
	Message string
}

type ResponseCall struct {
	Error  `json:"error,omitempty"`
	Result `json:"result,omitempty"`
}

type ResponseSend struct {
	Result bool `json:"result,omitempty"`
	Error  `json:"error,omitempty"`
}

var RequestSend = Request{
	Jsonrpc: "2.0",
	Id:      1,
	Method:  "eth_sendBundle",
	Params:  []Params{{}},
}

var RequestCall = Request{
	Jsonrpc: "2.0",
	Id:      1,
	Method:  "eth_callBundle",
	Params: []Params{
		{
			StateBlockNumber: "latest",
		},
	},
}

var RequestBundleStats = Request{
	Jsonrpc: "2.0",
	Id:      1,
	Method:  "flashbots_getBundleStats",
	Params: []Params{
		{},
	},
}

type Flashbot struct {
	prvKey *ecdsa.PrivateKey
	pubKey *common.Address

	// url for the relay, when not set it uses the default flashbot url.
	// Making it configurable allows using custom relays (i.e. ethermine).
	endpoint Endpoint
}

type Endpoint struct {
	URL                string
	SupportsSimulation bool
}

type Flashboter interface {
	SendBundle(txsHex []string, blockNumber uint64, timeout time.Duration) (*ResponseSend, error)
	CallBundle(txsHex []string, timeout time.Duration) (*ResponseCall, error)
	Endpoint() Endpoint
}

func DefaultEndpoint(netID int64) (*Endpoint, error) {
	url, err := relayURLDefault(netID)
	if err != nil {
		return nil, err
	}
	return &Endpoint{URL: url, SupportsSimulation: true}, nil
}

func NewAll(netID int64, prvKey *ecdsa.PrivateKey) ([]Flashboter, error) {
	var endpoints []Endpoint
	ep, err := DefaultEndpoint(netID)
	if err != nil {
		return nil, errors.Wrap(err, "create default endpoint")
	}
	endpoints = append(endpoints, *ep)

	switch netID {
	case 1:
		endpoints = append(endpoints, Endpoint{URL: "https://api.edennetwork.io/v1/bundle", SupportsSimulation: false})
		endpoints = append(endpoints, Endpoint{URL: "https://mev-relay.ethermine.org", SupportsSimulation: false})
	}
	return NewMulti(netID, prvKey, endpoints...)
}

func NewMulti(netID int64, prvKey *ecdsa.PrivateKey, endpoints ...Endpoint) ([]Flashboter, error) {
	if len(endpoints) < 1 {
		return nil, errors.New("should provide at least one endpoint")
	}
	var flashbots []Flashboter
	for _, endpoint := range endpoints {
		f, err := New(prvKey, endpoint)
		if err != nil {
			return nil, errors.Wrapf(err, "create flashbot instance:%v", endpoint.URL)
		}
		flashbots = append(flashbots, f)
	}
	return flashbots, nil
}

func New(prvKey *ecdsa.PrivateKey, endpoint Endpoint) (Flashboter, error) {
	if endpoint == (Endpoint{}) {
		return nil, errors.New("endpoint can't be empty")
	}
	fb := &Flashbot{
		endpoint: endpoint,
	}

	if prvKey != nil {
		return fb, fb.SetKey(prvKey)
	}
	return fb, nil
}

func (self *Flashbot) Endpoint() Endpoint {
	return self.endpoint
}

func (self *Flashbot) PrvKey() *ecdsa.PrivateKey {
	return self.prvKey
}

func (self *Flashbot) SetKey(prvKey *ecdsa.PrivateKey) error {
	self.prvKey = prvKey
	pubKeyE, ok := prvKey.Public().(*ecdsa.PublicKey)
	if !ok {
		return errors.New("casting private key to ECDSA")
	}
	pubKey := crypto.PubkeyToAddress(*pubKeyE)
	self.pubKey = &pubKey

	return nil
}

func (self *Flashbot) SendBundle(
	txsHex []string,
	blockNumber uint64,
	timeout time.Duration,
) (*ResponseSend, error) {
	r := RequestSend

	r.Params[0].BlockNumber = hexutil.EncodeUint64(blockNumber)
	r.Params[0].Txs = txsHex

	resp, err := self.req(r, timeout)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot send request")
	}

	rr, err := parseRespSend(resp, blockNumber)
	if err != nil {
		return nil, err
	}

	return rr, nil
}

func (self *Flashbot) CallBundle(
	txsHex []string,
	timeout time.Duration,
) (*ResponseCall, error) {
	if !self.endpoint.SupportsSimulation {
		return nil, errors.Errorf("doesn't support simulations relay:%v", self.endpoint.URL)
	}
	r := RequestCall

	blockDummy := uint64(100000000000000)

	r.Params[0].Txs = txsHex
	r.Params[0].BlockNumber = hexutil.EncodeUint64(blockDummy)

	resp, err := self.req(r, timeout)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot call request")
	}

	rr, err := parseRespCall(resp, blockDummy)
	if err != nil {
		return nil, err
	}

	return rr, nil
}

func (self *Flashbot) GetBundleStats(
	bundleHash string,
	blockNumber uint64,
	timeout time.Duration,
) (*ResultBundleStats, error) {
	r := RequestBundleStats
	r.Params[0].BundleHash = bundleHash
	r.Params[0].BlockNumber = hexutil.EncodeUint64(blockNumber)

	resp, err := self.req(r, timeout)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot stats request")
	}

	rr := &ResultBundleStats{}

	err = json.Unmarshal(resp, rr)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal flashbot bundle stats response")
	}

	if rr.Error.Code != 0 {
		return nil, errors.Errorf("flashbot request returned an error:%+v,%v", rr.Error, rr.Message)
	}

	return rr, nil

}

func parseRespSend(resp []byte, blockNum uint64) (*ResponseSend, error) {
	rrS := &ResponseSend{}
	rrC := &ResponseCall{}

	err := json.Unmarshal(resp, rrS)
	if err != nil {
		// Flashbot response is weird and sends different json structs.
		// Refactor when the flashbot team answers this issue https://github.com/flashbots/mev-relay-js/issues/66
		errC := json.Unmarshal(resp, rrC)
		if errC != nil {
			err := multierror.Append(err, errC)
			return nil, errors.Wrapf(err, "unmarshal flashbot call response:%v", string(resp))
		}
	}

	if rrS.Error.Code != 0 {
		errStr := fmt.Sprintf("flashbot request returned an error:%+v,%v block:%v", rrS.Error, rrS.Message, blockNum)
		return nil, errors.New(errStr)
	}
	if rrC.Error.Code != 0 {
		errStr := fmt.Sprintf("flashbot request returned an error:%+v,%v block:%v", rrC.Error, rrC.Message, blockNum)
		return nil, errors.New(errStr)
	}

	return rrS, nil
}

func parseRespCall(resp []byte, blockNum uint64) (*ResponseCall, error) {
	rr := &ResponseCall{
		Result: Result{},
	}

	err := json.Unmarshal(resp, rr)
	if err != nil {
		return nil, errors.Wrapf(err, "unmarshal flashbot call response:%v", string(resp))
	}

	if rr.Error.Code != 0 || (len(rr.Result.Results) > 0 && rr.Result.Results[0].Error != "") {
		errStr := fmt.Sprintf("flashbot request returned an error:%+v,%v block:%v", rr.Error, rr.Message, blockNum)
		if len(rr.Result.Results) > 0 {
			errStr += fmt.Sprintf(" Result:%+v , Revert:%+v, GasUsed:%+v", rr.Result.Results[0].Error, rr.Result.Results[0].Revert, rr.Result.Results[0].GasUsed)
		}
		return nil, errors.New(errStr)
	}

	return rr, nil
}

func (self *Flashbot) req(r Request, timeout time.Duration) ([]byte, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return nil, errors.Wrap(err, "marshaling flashbot tx params")
	}

	req, err := http.NewRequest("POST", self.endpoint.URL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, errors.Wrap(err, "creatting flashbot request")
	}
	signedP, err := signPayload(payload, self.prvKey, self.pubKey)
	if err != nil {
		return nil, errors.Wrap(err, "signing flashbot request")
	}
	req.Header.Add("content-type", "application/json")
	req.Header.Add("Accept", "application/json")
	req.Header.Add("X-Flashbots-Signature", signedP)

	mevHTTPClient := &http.Client{
		Timeout: timeout,
	}
	resp, err := mevHTTPClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot request")
	}
	res, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "reading flashbot reply")
	}

	if resp.StatusCode/100 != 2 {
		rbody, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, errors.Errorf("bad response status %v", resp.Status)
		}
		return nil, errors.Errorf("bad response resp status:%v  respBody:%v reqMethod:%+v", resp.Status, string(rbody)+string(res), r.Method)
	}
	err = resp.Body.Close()
	if err != nil {
		return nil, errors.Wrap(err, "closing flashbot reply body")
	}

	return res, nil
}

func NewSignedTXLegacy(
	netID int64,
	data []byte,
	gasLimit uint64,
	gasPrice *big.Int,
	to common.Address,
	nonce uint64,
	prvKey *ecdsa.PrivateKey,
) (string, *types.Transaction, error) {
	signer := types.LatestSignerForChainID(big.NewInt(netID))

	tx, err := types.SignNewTx(prvKey, signer, &types.AccessListTx{
		Gas:      gasLimit,
		GasPrice: gasPrice,
		To:       &to,
		ChainID:  big.NewInt(netID),
		Nonce:    nonce,
		Data:     data,
	})
	if err != nil {
		return "", nil, errors.Wrap(err, "sign transaction")
	}
	dataM, err := tx.MarshalBinary()
	if err != nil {
		return "", nil, errors.Wrap(err, "marshal tx data")
	}

	return hexutil.Encode(dataM), tx, nil
}

func NewSignedTX(
	netID int64,
	data []byte,
	gasLimit uint64,
	gasBaseFee *big.Int,
	gasTip *big.Int,
	to common.Address,
	nonce uint64,
	prvKey *ecdsa.PrivateKey,
) (string, *types.Transaction, error) {
	signer := types.LatestSignerForChainID(big.NewInt(netID))

	tx, err := types.SignNewTx(prvKey, signer, &types.DynamicFeeTx{
		ChainID:   big.NewInt(netID),
		Nonce:     nonce,
		GasFeeCap: big.NewInt(0).Add(gasBaseFee, gasTip),
		GasTipCap: gasTip,
		Gas:       gasLimit,
		To:        &to,
		Data:      data,
	})
	if err != nil {
		return "", nil, errors.Wrap(err, "sign transaction")
	}
	dataM, err := tx.MarshalBinary()
	if err != nil {
		return "", nil, errors.Wrap(err, "marshal tx data")
	}

	return hexutil.Encode(dataM), tx, nil
}

func signPayload(payload []byte, prvKey *ecdsa.PrivateKey, pubKey *common.Address) (string, error) {
	if prvKey == nil || pubKey == nil {
		return "", errors.New("private or public key is not set")
	}
	signature, err := crypto.Sign(
		accounts.TextHash([]byte(hexutil.Encode(crypto.Keccak256(payload)))),
		prvKey,
	)
	if err != nil {
		return "", errors.Wrap(err, "sign the payload")
	}

	return pubKey.Hex() +
		":" + hexutil.Encode(signature), nil
}

func relayURLDefault(netID int64) (string, error) {
	switch netID {
	case 1:
		return "https://relay.flashbots.net", nil
	case 5:
		return "https://relay-goerli.flashbots.net", nil
	default:
		return "", errors.Errorf("network id not supported id:%v", netID)
	}
}
