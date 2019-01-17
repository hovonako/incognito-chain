package metadata

import (
	"bytes"
	"encoding/hex"
	"math/big"

	"github.com/ninjadotorg/constant/common"
	"github.com/ninjadotorg/constant/database"
	"github.com/ninjadotorg/constant/privacy"
	"github.com/ninjadotorg/constant/wallet"
	"github.com/pkg/errors"
)

type CrowdsaleRequest struct {
	PaymentAddress privacy.PaymentAddress
	SaleID         []byte
	Info           []byte // offchain payment info (e.g. ETH/BTC txhash)

	Amount     *big.Int // amount of offchain asset (ignored if buying asset is not offchain)
	AssetPrice uint64   // ignored if buying asset is not offchain; otherwise, represents the price of buying asset; set by miner at mining time

	MetadataBase
}

func NewCrowdsaleRequest(csReqData map[string]interface{}) *CrowdsaleRequest {
	saleID, err := hex.DecodeString(csReqData["SaleId"].(string))
	if err != nil {
		return nil
	}
	info, err := hex.DecodeString(csReqData["Info"].(string))
	if err != nil {
		return nil
	}
	n := big.NewInt(0)
	n, ok := n.SetString(csReqData["Amount"].(string), 10)
	if !ok {
		n = big.NewInt(0)
	}
	result := &CrowdsaleRequest{
		PaymentAddress: csReqData["PaymentAddress"].(privacy.PaymentAddress),
		SaleID:         saleID,
		Info:           info,
		Amount:         n,
		AssetPrice:     0,
	}
	result.Type = CrowdSaleRequestMeta
	return result
}

func (csReq *CrowdsaleRequest) ValidateTxWithBlockChain(txr Transaction, bcr BlockchainRetriever, chainID byte, db database.DatabaseInterface) (bool, error) {
	// Check if sale exists and ongoing
	saleData, err := bcr.GetCrowdsaleData(csReq.SaleID)
	if err != nil {
		return false, err
	}
	// TODO(@0xbunyip): get height of beacon chain on new consensus
	height, err := bcr.GetTxChainHeight(txr)
	if err != nil || saleData.EndBlock >= height {
		return false, errors.Errorf("Crowdsale ended")
	}

	// Check if Payment address is DCB's
	keyWalletDCBAccount, _ := wallet.Base58CheckDeserialize(common.DCBAddress)
	if !bytes.Equal(csReq.PaymentAddress.Pk[:], keyWalletDCBAccount.KeySet.PaymentAddress.Pk[:]) || !bytes.Equal(csReq.PaymentAddress.Tk[:], keyWalletDCBAccount.KeySet.PaymentAddress.Tk[:]) {
		return false, errors.Errorf("Crowdsale request must send CST to DCBAddress")
	}
	return true, nil
}

func (csReq *CrowdsaleRequest) ValidateSanityData(bcr BlockchainRetriever, txr Transaction) (bool, bool, error) {
	if len(csReq.PaymentAddress.Pk) == 0 {
		return false, false, errors.New("Wrong request info's payment address")
	}
	return false, true, nil
}

func (csReq *CrowdsaleRequest) ValidateMetadataByItself() bool {
	// The validation just need to check at tx level, so returning true here
	return true
}

func (csReq *CrowdsaleRequest) Hash() *common.Hash {
	record := csReq.PaymentAddress.String()
	record += string(csReq.SaleID)
	record += string(csReq.Info)
	record += string(csReq.Amount.String())
	record += string(csReq.AssetPrice)

	// final hash
	record += csReq.MetadataBase.Hash().String()
	hash := common.DoubleHashH([]byte(record))
	return &hash
}
