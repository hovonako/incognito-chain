package metadata

import (
	"fmt"

	"github.com/ninjadotorg/constant/common"
	"github.com/ninjadotorg/constant/database"
	"github.com/ninjadotorg/constant/privacy"
)

type DividendPayment struct {
	TokenHolder privacy.PaymentAddress
	Amount      uint64
}

type Dividend struct {
	PayoutID       uint64
	TokenID        *common.Hash
	PaymentAddress privacy.PaymentAddress

	MetadataBase
}

func (div *Dividend) Hash() *common.Hash {
	record := fmt.Sprintf("%d", div.PayoutID)
	record += div.TokenID.String()
	record += div.PaymentAddress.String()

	// final hash
	record += div.MetadataBase.Hash().String()
	hash := common.DoubleHashH([]byte(record))
	return &hash
}

func (div *Dividend) ValidateTxWithBlockChain(txr Transaction, bcr BlockchainRetriever, shardID byte, db database.DatabaseInterface) (bool, error) {
	// Check if there's a proposal to pay dividend
	// TODO(@0xbunyip): get current proposal and check if it is dividend payout
	//	proposal := &DividendProposal{}
	//	_, tokenHolders, correctAmounts, err := bcr.GetAmountPerAccount(proposal)
	//	if err != nil {
	//		return false, err
	//	}
	//
	//	// Check if user is not rewarded and amount is correct
	//	receivers, recAmounts := txr.GetReceivers()
	//	for j, rec := range receivers {
	//		// Check amount
	//		count := 0
	//		for i, holder := range tokenHolders {
	//			temp, _ := hex.DecodeString(holder)
	//			paymentAddress := (&privacy.PaymentAddress{}).SetBytes(temp)
	//			if bytes.Equal(paymentAddress.Pk[:], rec) {
	//				count += 1
	//				if correctAmounts[i] != recAmounts[j] {
	//					return false, fmt.Errorf("Payment amount for user %s incorrect, found %d instead of %d", holder, recAmounts[j], correctAmounts[i])
	//				}
	//			}
	//		}
	//
	//		if count == 0 {
	//			return false, fmt.Errorf("User %s isn't eligible for receiving dividend", rec)
	//		} else if count > 1 {
	//			return false, fmt.Errorf("Multiple dividend payments found for user %s", rec)
	//		}
	//	}
	return false, nil
}

func (div *Dividend) ValidateSanityData(bcr BlockchainRetriever, txr Transaction) (bool, bool, error) {
	return false, true, nil // No need to check for fee
}

func (div *Dividend) ValidateMetadataByItself() bool {
	return true
}

// CheckTransactionFee returns true since loan response tx doesn't have fee
func (div *Dividend) CheckTransactionFee(tr Transaction, minFee uint64) bool {
	return true
}
