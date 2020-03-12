package relayer

import (
	"context"
	"errors"
	"math/big"
	"os"
	"strconv"

	ether "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	relayerAbi "github.com/tomochain/tomox-sdk/relayer/abi"
	"github.com/tomochain/tomox-sdk/utils"
)

var logger = utils.Logger

// Blockchain struct
type Blockchain struct {
	client    *rpc.Client
	ethclient *ethclient.Client
	signer    *Signer
}

// PairToken pare token
type PairToken struct {
	BaseToken  common.Address
	QuoteToken common.Address
}

// LendingPairToken lending pari
type LendingPairToken struct {
	Term         uint64
	LendingToken common.Address
}

// TokenInfo token info
type TokenInfo struct {
	Name     string
	Symbol   string
	Decimals uint8
	address  common.Address
}

// RInfo struct
type RInfo struct {
	Tokens  map[common.Address]*TokenInfo
	Pairs   []*PairToken
	MakeFee uint16
	TakeFee uint16
}

// LendingRInfo lending relayer info
type LendingRInfo struct {
	Tokens       map[common.Address]*TokenInfo
	LendingPairs []*LendingPairToken
	Fee          uint16
}

// NewBlockchain init
func NewBlockchain(client *rpc.Client,
	ethclient *ethclient.Client,
	signer *Signer) *Blockchain {

	return &Blockchain{
		client:    client,
		ethclient: ethclient,
		signer:    signer,
	}
}

func (b *Blockchain) abiFrom(abiPath string) (*abi.ABI, error) {
	file, err := os.Open(abiPath)
	if err != nil {
		return nil, err
	}
	parsed, err := abi.JSON(file)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// RunContract run smart contract
func (b *Blockchain) RunContract(contractAddr common.Address, abi *abi.ABI, method string, args ...interface{}) (interface{}, error) {
	input, err := abi.Pack(method)
	if err != nil {
		return nil, err
	}

	msg := ether.CallMsg{To: &contractAddr, Data: input}
	result, err := b.ethclient.CallContract(context.Background(), msg, nil)
	if err != nil {
		logger.Error(err)
	}
	var unpackResult interface{}
	err = abi.Unpack(&unpackResult, method, result)
	if err != nil {
		return nil, err
	}
	return unpackResult, nil
}

// GetTokenInfoEx return token info
func (b *Blockchain) GetTokenInfoEx(token common.Address, abiPath string) (*TokenInfo, error) {
	abi, err := b.abiFrom(abiPath)
	if err != nil {
		return nil, err
	}
	return b.GetTokenInfo(token, abi)
}

// GetTokenInfo return token info
func (b *Blockchain) GetTokenInfo(token common.Address, abi *abi.ABI) (*TokenInfo, error) {

	result, err := b.RunContract(token, abi, "name")
	if err != nil {
		return nil, err
	}
	name := result.(string)
	result, err = b.RunContract(token, abi, "symbol")
	if err != nil {
		return nil, err
	}
	symbol := result.(string)
	result, err = b.RunContract(token, abi, "decimals")
	if err != nil {
		return nil, err
	}
	decimals := result.(uint8)

	return &TokenInfo{
		Name:     name,
		Symbol:   symbol,
		Decimals: decimals,
	}, nil
}

func (b *Blockchain) setBaseTokenInfo() *TokenInfo {
	return &TokenInfo{
		Name:     "TOMO",
		Symbol:   "TOMO",
		Decimals: 18,
	}
}

// GetRelayer return all tokens in smart contract
func (b *Blockchain) GetRelayer(coinAddress common.Address, contractAddress common.Address) (*RInfo, error) {
	abiRelayer, err := relayerAbi.GetRelayerAbi()
	if err != nil {
		return nil, err
	}
	abiToken, err := relayerAbi.GetTokenAbi()
	if err != nil {
		return nil, err
	}
	input, err := abiRelayer.Pack("getRelayerByCoinbase", coinAddress)
	if err != nil {
		return nil, err
	}

	msg := ether.CallMsg{To: &contractAddress, Data: input}
	result, err := b.ethclient.CallContract(context.Background(), msg, nil)
	if err != nil {
		logger.Error(err)
		return nil, err
	}
	logger.Debug("data: ", result)

	relayerInfo := RInfo{
		Tokens: make(map[common.Address]*TokenInfo),
	}
	if method, ok := abiRelayer.Methods["getRelayerByCoinbase"]; ok {
		contractData, err := method.Outputs.UnpackValues(result)
		if err == nil {
			if len(contractData) == 6 {
				relayerInfo.MakeFee = contractData[3].(uint16)
				relayerInfo.TakeFee = contractData[3].(uint16)
				fromTokens := contractData[4].([]common.Address)
				toTokens := contractData[5].([]common.Address)
				setToken := utils.Union(fromTokens, toTokens)
				logger.Debug("Relayer data:", fromTokens, toTokens)
				for _, t := range setToken {
					if utils.IsNativeTokenByAddress(t) {
						tokenInfo := b.setBaseTokenInfo()
						relayerInfo.Tokens[t] = tokenInfo
					} else {
						tokenInfo, err := b.GetTokenInfo(t, &abiToken)
						if err != nil {
							return nil, err
						}
						relayerInfo.Tokens[t] = tokenInfo
						logger.Debug("Token data:", tokenInfo.Name, tokenInfo.Symbol, tokenInfo.address)
					}

				}
				if len(fromTokens) == len(toTokens) {
					for i, v := range fromTokens {
						base := v
						quote := toTokens[i]

						pairToken := &PairToken{
							BaseToken:  base,
							QuoteToken: quote,
						}
						relayerInfo.Pairs = append(relayerInfo.Pairs, pairToken)
					}
				}

			}
		}
	} else {
		return &relayerInfo, errors.New("Can not get relayer information")
	}

	return &relayerInfo, nil
}

// GetLendingRelayer return all lending pair in smart contract
func (b *Blockchain) GetLendingRelayer(coinAddress common.Address, contractAddress common.Address) (*LendingRInfo, error) {
	logger.Debug("GetLendingRelayer:", coinAddress.Hex(), contractAddress.Hex())
	abiRelayer, err := relayerAbi.GetLendingAbi()
	if err != nil {
		return nil, err
	}
	abiToken, err := relayerAbi.GetTokenAbi()
	if err != nil {
		return nil, err
	}
	input, err := abiRelayer.Pack("getLendingRelayerByCoinbase", coinAddress)
	if err != nil {
		return nil, err
	}

	msg := ether.CallMsg{To: &contractAddress, Data: input}
	result, err := b.ethclient.CallContract(context.Background(), msg, nil)
	if err != nil {
		logger.Error(err)
		return nil, err
	}
	logger.Debug("lending relayer data: ", result)

	lendingRInfo := LendingRInfo{
		Tokens: make(map[common.Address]*TokenInfo),
	}
	if method, ok := abiRelayer.Methods["getLendingRelayerByCoinbase"]; ok {
		contractData, err := method.Outputs.UnpackValues(result)
		if err == nil {
			if len(contractData) == 4 {
				lendingRInfo.Fee = contractData[0].(uint16)
				termList := contractData[2].([]*big.Int)
				lendingTokenList := contractData[1].([]common.Address)
				setLendingToken := utils.Union(lendingTokenList, lendingTokenList)
				for _, t := range setLendingToken {
					if utils.IsNativeTokenByAddress(t) {
						tokenInfo := b.setBaseTokenInfo()
						lendingRInfo.Tokens[t] = tokenInfo
					} else {
						tokenInfo, err := b.GetTokenInfo(t, &abiToken)
						if err != nil {
							return nil, err
						}
						lendingRInfo.Tokens[t] = tokenInfo
						logger.Debug("Token data:", tokenInfo.Name, tokenInfo.Symbol)
					}

				}
				if len(termList) == len(lendingTokenList) {
					for i, v := range termList {
						t, err := strconv.ParseUint(v.String(), 10, 64)
						if err != nil {
							return &lendingRInfo, err
						}
						pairToken := &LendingPairToken{
							Term:         t,
							LendingToken: lendingTokenList[i],
						}
						lendingRInfo.LendingPairs = append(lendingRInfo.LendingPairs, pairToken)
					}
				}

			}
		}
	} else {
		return &lendingRInfo, errors.New("Can not get relayer information")
	}

	return &lendingRInfo, nil
}
