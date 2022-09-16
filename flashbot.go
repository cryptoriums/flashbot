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
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pkg/errors"
)

type Flashboter interface {
	SendPrivateTransaction(ctx context.Context, txHex string, blockNum uint64, fast bool) (*SendPrivateTransactionResponse, error)
	CancelPrivateTransaction(ctx context.Context, txHash common.Hash) (*CancelPrivateTransactionResponse, error)
	SendBundle(ctx context.Context, txsHex []string, blockNum uint64) (*Response, error)
	CallBundle(ctx context.Context, txsHex []string, blockNumState uint64) (*Response, error)
	GetBundleStats(ctx context.Context, bundleHash string, blockNum uint64) (*ResultBundleStats, error)
	GetUserStats(ctx context.Context, blockNum uint64) (*ResultUserStats, error)
	EstimateGasBundle(ctx context.Context, txs []Tx, blockNum uint64) (*Response, error)
	Api() *Api
}

type Params struct {
	BlockNum      string `json:"blockNumber,omitempty"`
	StateBlockNum string `json:"stateBlockNumber,omitempty"`
}

type ParamsSendCall struct {
	Params
	Txs []string `json:"txs,omitempty"`
}

type ParamsPrivateTransaction struct {
	Tx             string `json:"tx,omitempty"`
	МaxBlockNumber string `json:"maxBlockNumber,omitempty"`
	Preferences    struct {
		Fast bool `json:"fast,omitempty"`
	} `json:"preferences,omitempty"`
}

type ParamsCancelPrivateTransaction struct {
	TxHash string `json:"txHash,omitempty"`
}

type ParamsBundleStats struct {
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

type ResultUserStats struct {
	Error
	Result BundleUserStats
}

type BundleUserStats struct {
	IsHighPriority       bool   `json:"is_high_priority,omitempty"`
	AllTimeMinerPayments string `json:"all_time_miner_payments,omitempty"`
	AllTimeGasSimulated  string `json:"all_time_gas_simulated,omitempty"`
	Last7dMinerPayments  string `json:"last_7d_miner_payments,omitempty"`
	Last7dGasSimulated   string `json:"last_7d_gas_simulated,omitempty"`
	Last1dMinerPayments  string `json:"last_1d_miner_payments,omitempty"`
	Last1dGasSimulated   string `json:"last_1d_gas_simulated,omitempty"`
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
	blockNum uint64,
) (*Response, error) {
	method := "eth_estimateGasBundle"
	if self.api.MethodSend != "" {
		method = self.api.MethodSend
	}

	param := ParamsGasEstimate{
		Txs: txs,
		Params: Params{
			StateBlockNum: "latest",
			BlockNum:      hexutil.EncodeUint64(blockNum),
		},
	}

	resp, err := self.req(ctx, method, param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot send request")
	}

	rr, err := parseResp(resp, blockNum)
	if err != nil {
		return nil, err
	}

	return rr, nil
}

type SendPrivateTransactionResponse struct {
	Error  `json:"error,omitempty"`
	Result string `json:"result,omitempty"`
}

type CancelPrivateTransactionResponse struct {
	Error  `json:"error,omitempty"`
	Result bool `json:"result,omitempty"`
}

func (self *Flashbot) SendPrivateTransaction(ctx context.Context, txHex string, blockNum uint64, fast bool) (*SendPrivateTransactionResponse, error) {
	param := ParamsPrivateTransaction{
		Tx:             txHex,
		МaxBlockNumber: hexutil.EncodeUint64(blockNum),
	}
	resp, err := self.req(ctx, "eth_sendPrivateTransaction", param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot private TX request")
	}

	rr := &SendPrivateTransactionResponse{}

	err = json.Unmarshal(resp, rr)
	if err != nil {
		return nil, errors.Wrapf(err, "unmarshal flashbot response:%v", string(resp))
	}

	if rr.Error.Code != 0 {
		errStr := fmt.Sprintf("flashbot request returned an error:%+v,%v block:%v", rr.Error, rr.Message, blockNum)
		return nil, errors.New(errStr)
	}

	return rr, nil
}

func (self *Flashbot) CancelPrivateTransaction(ctx context.Context, txHash common.Hash) (*CancelPrivateTransactionResponse, error) {
	param := ParamsCancelPrivateTransaction{
		TxHash: txHash.Hex(),
	}
	resp, err := self.req(ctx, "eth_cancelPrivateTransaction", param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot cancel pivate TX request")
	}

	rr := &CancelPrivateTransactionResponse{}

	err = json.Unmarshal(resp, rr)
	if err != nil {
		return nil, errors.Wrapf(err, "unmarshal flashbot response:%v", string(resp))
	}

	if rr.Error.Code != 0 {
		errStr := fmt.Sprintf("flashbot request returned an error:%+v,%v", rr.Error, rr.Message)
		return nil, errors.New(errStr)
	}

	return rr, nil
}

func (self *Flashbot) SendBundle(
	ctx context.Context,
	txsHex []string,
	blockNum uint64,
) (*Response, error) {
	method := "eth_sendBundle"
	if self.api.MethodSend != "" {
		method = self.api.MethodSend
	}

	param := ParamsSendCall{
		Txs: txsHex,
		Params: Params{
			StateBlockNum: "latest",
			BlockNum:      hexutil.EncodeUint64(blockNum),
		},
	}

	resp, err := self.req(ctx, method, param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot send request")
	}

	rr, err := parseResp(resp, blockNum)
	if err != nil {
		return nil, err
	}

	return rr, nil
}

func (self *Flashbot) CallBundle(
	ctx context.Context,
	txsHex []string,
	_blockNumState uint64,
) (*Response, error) {
	if !self.api.SupportsSimulation {
		return nil, errors.Errorf("doesn't support simulations relay:%v", self.api.URL)
	}

	method := "eth_callBundle"
	if self.api.MethodSend != "" {
		method = self.api.MethodSend
	}

	blockDummy := uint64(100000000000000)
	blockNumState := "latest"
	if _blockNumState != 0 {
		blockNumState = hexutil.EncodeUint64(_blockNumState)
	}
	param := ParamsSendCall{
		Txs: txsHex,
		Params: Params{
			StateBlockNum: blockNumState,
			BlockNum:      hexutil.EncodeUint64(blockDummy)},
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
	blockNum uint64,
) (*ResultBundleStats, error) {

	param := ParamsBundleStats{
		BundleHash: bundleHash,
		Params:     Params{BlockNum: hexutil.EncodeUint64(blockNum)},
	}

	resp, err := self.req(ctx, "flashbots_getBundleStats", param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot bundle stats request")
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

func (self *Flashbot) GetUserStats(
	ctx context.Context,
	blockNum uint64,
) (*ResultUserStats, error) {

	param := hexutil.EncodeUint64(blockNum)

	resp, err := self.req(ctx, "flashbots_getUserStats", param)
	if err != nil {
		return nil, errors.Wrap(err, "flashbot user stats request")
	}

	rr := &ResultUserStats{}

	err = json.Unmarshal(resp, rr)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal flashbot user stats response")
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

	if resp.StatusCode/100 != 2 {
		respDump, err := httputil.DumpResponse(resp, true)
		if err != nil {
			return nil, errors.Errorf("bad response status %v", resp.Status)
		}
		reqDump, err := httputil.DumpRequestOut(req, true)
		if err != nil {
			return nil, errors.Errorf("bad response resp respDump:%v", string(respDump))
		}
		return nil, errors.Errorf("bad response resp respDump:%v reqDump:%v", string(respDump), string(reqDump))
	}

	res, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "reading flashbot reply")
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
