// Copyright (c) The Cryptorium Authors.
// Licensed under the MIT License.

// Package flashbot provides a structured way to send TX to the flashbot relays.
package flashbot

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/tls"
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
	"github.com/pkg/errors"
)

type Flashboter interface {
	SendBundle(ctx context.Context, txsHex []string, blockNumber uint64) (*Response, error)
	CallBundle(ctx context.Context, txsHex []string) (*Response, error)
	GetBundleStats(ctx context.Context, bundleHash string, blockNumber uint64) (*ResultBundleStats, error)
	EstimateGasBundle(ctx context.Context, txs []Tx, blockNumber uint64) (*Response, error)
	Api() *Api
}

type Params struct {
	BlockNumber      string `json:"blockNumber,omitempty"`
	StateBlockNumber string `json:"stateBlockNumber,omitempty"`
}

type ParamsSendCall struct {
	Params
	Txs []string `json:"txs,omitempty"`
}

type ParamsStats struct {
	Params
	BundleHash string `json:"bundleHash,omitempty"`
}

type Tx struct {
	From common.Address `json:"from,omitempty"`
	To   common.Address `json:"to,omitempty"`
	Data []byte         `json:"data,omitempty"`
}

type ParamsGasEstimate struct {
	Params
	Txs []Tx `json:"txs,omitempty"`
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

type Response struct {
	Error  `json:"error,omitempty"`
	Result `json:"result,omitempty"`
}

type Flashbot struct {
	prvKey *ecdsa.PrivateKey
	pubKey *common.Address

	// The api spec for the relay.
	// Different relays use different api method names and this allows making it configurable.
	api *Api
}

type Api struct {
	URL                string
	SupportsSimulation bool
	MethodCall         string
	MethodSend         string
	CustomHeaders      map[string]string
}

func DefaultApi(netID int64) (*Api, error) {
	url, err := relayURLDefault(netID)
	if err != nil {
		return nil, err
	}
	return &Api{URL: url, SupportsSimulation: true}, nil
}

func NewAll(netID int64, prvKey *ecdsa.PrivateKey) ([]Flashboter, error) {
	var apis []*Api
	ep, err := DefaultApi(netID)
	if err != nil {
		return nil, errors.Wrap(err, "create default api")
	}
	apis = append(apis, ep)

	switch netID {
	case 1:
		apis = append(apis, &Api{URL: "https://api.edennetwork.io/v1/bundle", SupportsSimulation: false})
		apis = append(apis, &Api{URL: "https://mev-relay.ethermine.org", SupportsSimulation: false})
		apis = append(apis, &Api{URL: "https://bundle.miningdao.io", SupportsSimulation: false})
	}
	return NewMulti(netID, prvKey, apis...)
}

func NewMulti(netID int64, prvKey *ecdsa.PrivateKey, apis ...*Api) ([]Flashboter, error) {
	if len(apis) < 1 {
		return nil, errors.New("should provide at least one api")
	}
	var flashbots []Flashboter
	for _, api := range apis {
		f, err := New(prvKey, api)
		if err != nil {
			return nil, errors.Wrapf(err, "create flashbot instance:%v", api.URL)
		}
		flashbots = append(flashbots, f)
	}
	return flashbots, nil
}

func New(prvKey *ecdsa.PrivateKey, api *Api) (Flashboter, error) {
	if api == nil {
		return nil, errors.New("api can't be empty")
	}

	fb := &Flashbot{
		api: api,
	}

	if prvKey != nil {
		return fb, fb.SetKey(prvKey)
	}
	return fb, nil
}

func (self *Flashbot) Api() *Api {
	return self.api
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

func (self *Flashbot) EstimateGasBundle(
	ctx context.Context,
	txs []Tx,
	blockNumber uint64,
) (*Response, error) {
	method := "eth_estimateGasBundle"
	if self.api.MethodSend != "" {
		method = self.api.MethodSend
	}

	param := ParamsGasEstimate{
		Txs: txs,
		Params: Params{
			StateBlockNumber: "latest",
			BlockNumber:      hexutil.EncodeUint64(blockNumber),
		},
	}

	resp, err := self.req(ctx, method, param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot send request")
	}

	rr, err := parseResp(resp, blockNumber)
	if err != nil {
		return nil, err
	}

	return rr, nil
}

func (self *Flashbot) SendBundle(
	ctx context.Context,
	txsHex []string,
	blockNumber uint64,
) (*Response, error) {
	method := "eth_sendBundle"
	if self.api.MethodSend != "" {
		method = self.api.MethodSend
	}

	param := ParamsSendCall{
		Txs: txsHex,
		Params: Params{
			StateBlockNumber: "latest",
			BlockNumber:      hexutil.EncodeUint64(blockNumber),
		},
	}

	resp, err := self.req(ctx, method, param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot send request")
	}

	rr, err := parseResp(resp, blockNumber)
	if err != nil {
		return nil, err
	}

	return rr, nil
}

func (self *Flashbot) CallBundle(
	ctx context.Context,
	txsHex []string,
) (*Response, error) {
	if !self.api.SupportsSimulation {
		return nil, errors.Errorf("doesn't support simulations relay:%v", self.api.URL)
	}

	method := "eth_callBundle"
	if self.api.MethodSend != "" {
		method = self.api.MethodSend
	}

	blockDummy := uint64(100000000000000)

	param := ParamsSendCall{
		Txs: txsHex,
		Params: Params{
			StateBlockNumber: "latest",
			BlockNumber:      hexutil.EncodeUint64(blockDummy)},
	}

	resp, err := self.req(ctx, method, param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot call request")
	}

	rr, err := parseResp(resp, blockDummy)
	if err != nil {
		return nil, err
	}

	return rr, nil
}

func (self *Flashbot) GetBundleStats(
	ctx context.Context,
	bundleHash string,
	blockNumber uint64,
) (*ResultBundleStats, error) {

	param := ParamsStats{
		BundleHash: bundleHash,
		Params:     Params{BlockNumber: hexutil.EncodeUint64(blockNumber)},
	}

	resp, err := self.req(ctx, "flashbots_getBundleStats", param)
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

func parseResp(resp []byte, blockNum uint64) (*Response, error) {
	rr := &Response{
		Result: Result{},
	}

	err := json.Unmarshal(resp, rr)
	if err != nil {
		return nil, errors.Wrapf(err, "unmarshal flashbot response:%v", string(resp))
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

func (self *Flashbot) req(ctx context.Context, method string, params ...interface{}) ([]byte, error) {
	msg, err := newMessage(method, params...)
	if err != nil {
		return nil, errors.Wrap(err, "marshaling flashbot tx params")
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	// return nil, errors.New("payload" + string(payload))

	req, err := http.NewRequestWithContext(ctx, "POST", self.api.URL, ioutil.NopCloser(bytes.NewReader(payload)))
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

	for n, v := range self.api.CustomHeaders {
		req.Header.Add(n, v)
	}

	mevHTTPClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
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
		return nil, errors.Errorf("bad response resp status:%v  respBody:%v reqMethod:%+v", resp.Status, string(rbody)+string(res), method)
	}
	err = resp.Body.Close()
	if err != nil {
		return nil, errors.Wrap(err, "closing flashbot reply body")
	}

	return res, nil
}

// A value of this type can a JSON-RPC request, notification, successful response or
// error response. Which one it is depends on the fields.
type jsonrpcMessage struct {
	Version string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   *jsonError      `json:"error,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

type jsonError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func newMessage(method string, paramsIn ...interface{}) (*jsonrpcMessage, error) {
	msg := &jsonrpcMessage{Version: "2.0", ID: []byte(`1`), Method: method}
	if paramsIn != nil { // prevent sending "params":null
		var err error
		if msg.Params, err = json.Marshal(paramsIn); err != nil {
			return nil, err
		}
	}
	return msg, nil
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
