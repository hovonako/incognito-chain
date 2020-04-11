package transaction

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/incognitochain/incognito-chain/privacy/coin"
	errhandler "github.com/incognitochain/incognito-chain/privacy/errorhandler"
	"github.com/incognitochain/incognito-chain/privacy/key"

	"github.com/incognitochain/incognito-chain/common"
	"github.com/incognitochain/incognito-chain/dataaccessobject/statedb"
	"github.com/incognitochain/incognito-chain/privacy"
	"github.com/incognitochain/incognito-chain/privacy/operation"
	"github.com/incognitochain/incognito-chain/privacy/privacy_v2"
	"github.com/incognitochain/incognito-chain/privacy/privacy_v2/mlsag"
)

type TxVersion2 struct{}

type TxSignatureVer2 struct {
	ring    *mlsag.Ring
	indexes [][]*big.Int
}

func (txSig TxSignatureVer2) GetRing() *mlsag.Ring     { return txSig.ring }
func (txSig TxSignatureVer2) GetIndexes() [][]*big.Int { return txSig.indexes }

func (txSig *TxSignatureVer2) SetRing(r *mlsag.Ring)       { txSig.ring = r }
func (txSig *TxSignatureVer2) SetIndexes(idx [][]*big.Int) { txSig.indexes = idx }

func (txSig *TxSignatureVer2) Init() *TxSignatureVer2 {
	if txSig == nil {
		txSig = new(TxSignatureVer2)
	}
	txSig.ring = new(mlsag.Ring)
	txSig.indexes = make([][]*big.Int, 0)
	return txSig
}

func (txSig TxSignatureVer2) ToBytes() ([]byte, error) {
	byteMlsag, err := txSig.ring.ToBytes()
	if err != nil {
		Logger.Log.Errorf("Error when getting byte of ring, error %v ", err)
		return nil, err
	}
	if len(byteMlsag) > MaxSizeUint32 {
		return nil, errors.New("Length of mlsag is too big, larger than 1<<32")
	}
	n := len(txSig.indexes)
	if n == 0 {
		return nil, errors.New("TxSig.ToBytes: Indexes is empty")
	}
	if n > MaxSizeByte {
		return nil, errors.New("TxSig.ToBytes: Indexes is too large, too many rows")
	}
	m := len(txSig.indexes[0])
	if m > MaxSizeByte {
		return nil, errors.New("TxSig.ToBytes: Indexes is too large, too many columns")
	}
	for i := 1; i < n; i += 1 {
		if len(txSig.indexes[i]) != m {
			return nil, errors.New("TxSig.ToBytes: Indexes is not a rectangle array")
		}
	}

	mlsagLen := uint32(len(byteMlsag))
	b := make([]byte, 0)
	b = append(b, common.Uint32ToBytes(mlsagLen)...)
	b = append(b, byteMlsag...)

	b = append(b, byte(n))
	b = append(b, byte(m))
	for i := 0; i < n; i += 1 {
		for j := 0; j < m; j += 1 {
			// 8 bytes for uint64
			currentByte := common.AddPaddingBigInt(txSig.indexes[i][j], common.Uint64Size)
			b = append(b, currentByte...)
		}
	}
	return b, nil
}

func (txSig *TxSignatureVer2) FromBytes(b []byte) error {
	if len(b) < 4 {
		return errors.New("TxSig.FromBytes: cannot parse ring length, length of byte is too small")
	}

	ringLenByte := b[0:common.Uint32Size]
	ringLen, err := common.BytesToUint32(ringLenByte)
	if err != nil {
		Logger.Log.Errorf("Parsing bytes to uint32 error %v ", err)
		return err
	}
	offset := uint32(common.Uint32Size)
	if offset+ringLen > uint32(len(b)) {
		return errors.New("TxSig.FromBytes: cannot parse mlsag ring, length of byte is too small")
	}
	ringByte := b[offset : offset+ringLen]
	ring, err := new(mlsag.Ring).FromBytes(ringByte)
	if err != nil {
		Logger.Log.Errorf("Parsing bytes to mlsagring error %v ", err)
		return err
	}
	offset += ringLen

	if offset+2 > uint32(len(b)) {
		return errors.New("TxSig.FromBytes: cannot parse length of indexes, length of byte is too small")
	}
	n := int(b[offset : offset+1][0])
	m := int(b[offset+1 : offset+2][0])
	offset += 2
	if int(offset)+common.Uint64Size*n*m > len(b) {
		return errors.New("TxSig.FromBytes: cannot parse indexes, length of byte is too small")
	}
	indexes := make([][]*big.Int, n)
	for i := 0; i < n; i += 1 {
		row := make([]*big.Int, m)
		for j := 0; j < m; j += 1 {
			currentByte := b[offset : offset+common.Uint64Size]
			offset += common.Uint64Size
			row[j] = new(big.Int).SetBytes(currentByte)
		}
		indexes[i] = row
	}

	if txSig == nil {
		txSig = new(TxSignatureVer2)
	}
	txSig.SetRing(ring)
	txSig.SetIndexes(indexes)
	return nil
}

func generateMlsagRingWithIndexes(inp *[]*coin.InputCoin, out *[]*coin.OutputCoin, params *TxPrivacyInitParams, pi int, shardID byte, ringSize int) (*mlsag.Ring, [][]*big.Int, error) {
	inputCoins := *inp
	outputCoins := *out

	// Remember which coin commitment existed in inputCoin
	// The coinCommitment is in the original format (have not changed it to ver2)
	// The reason why it must be in original format is that we will query db these commitments
	listUsableCommitments := make(map[common.Hash][]byte)
	for _, in := range inputCoins {
		usableCommitment := in.CoinDetails.GetCoinCommitment().ToBytesS()
		commitmentInHash := common.HashH(usableCommitment)
		listUsableCommitments[commitmentInHash] = usableCommitment
	}
	lenCommitment, err := statedb.GetCommitmentLength(params.stateDB, *params.tokenID, shardID)
	if err != nil {
		Logger.Log.Errorf("Getting length of commitment error %v ", err)
		return nil, nil, err
	}
	if lenCommitment == nil {
		Logger.Log.Error(errors.New("Commitments is empty"))
		return nil, nil, errors.New("Commitments is empty")
	}

	outputCommitments := new(operation.Point).Identity()
	for i := 0; i < len(outputCoins); i += 1 {
		commitment := coin.ParseCommitmentToV2WithCoin(outputCoins[i].CoinDetails)
		outputCommitments.Add(outputCommitments, commitment)
	}

	ring := make([][]*operation.Point, ringSize)
	key := params.senderSK

	feeCommitment := new(operation.Point).ScalarMult(
		operation.PedCom.G[operation.PedersenValueIndex],
		new(operation.Scalar).FromUint64(params.fee),
	)

	// The indexes array is for validator recheck
	indexes := make([][]*big.Int, ringSize)
	for i := 0; i < ringSize; i += 1 {
		sumInputs := new(operation.Point).Identity()
		sumInputs.Sub(sumInputs, feeCommitment)

		row := make([]*operation.Point, len(inputCoins))
		rowIndexes := make([]*big.Int, len(inputCoins))

		if i == pi {
			for j := 0; j < len(inputCoins); j += 1 {
				privKey := new(operation.Scalar).FromBytesS(*key)
				row[j] = new(operation.Point).ScalarMultBase(privKey)

				coinCommitmentV2 := coin.ParseCommitmentToV2WithCoin(inputCoins[j].CoinDetails)
				sumInputs.Add(sumInputs, coinCommitmentV2)

				// Store index for validator recheck
				coinCommitmentDB := inputCoins[j].CoinDetails.GetCoinCommitment()
				commitmentBytes := coinCommitmentDB.ToBytesS()
				rowIndexes[j], err = statedb.GetCommitmentIndex(params.stateDB, *params.tokenID, commitmentBytes, shardID)
				if err != nil {
					Logger.Log.Errorf("Getting commitment index error %v ", err)
					return nil, nil, err
				}
			}
		} else {
			for j := 0; j < len(inputCoins); j += 1 {
				for {
					index, _ := common.RandBigIntMaxRange(lenCommitment)
					rowIndexes[j] = index

					ok, err := statedb.HasCommitmentIndex(params.stateDB, *params.tokenID, index.Uint64(), shardID)
					if !ok || err != nil {
						Logger.Log.Errorf("Has commitment index error %v ", err)
						return nil, nil, err
					}
					commitment, publicKey, snd, err := statedb.GetCommitmentPublicKeyAddditionalByIndex(params.stateDB, *params.tokenID, index.Uint64(), shardID)
					if err != nil {
						Logger.Log.Errorf("Get Commitment PublicKey and Additional by index error %v ", err)
						return nil, nil, err
					}
					_, found := listUsableCommitments[common.HashH(commitment)]
					if found && (lenCommitment.Uint64() != 1 || len(inputCoins) != 1) {
						continue
					}
					row[j], err = new(operation.Point).FromBytesS(publicKey)
					if err != nil {
						fmt.Println(publicKey)
						Logger.Log.Errorf("Parsing from byte to point error %v ", err)
						return nil, nil, err
					}

					// Change commitment to v2
					commitmentBytesV2, err := coin.ParseCommitmentToV2ByBytes(
						commitment,
						publicKey,
						snd,
						shardID,
					)
					if err != nil {
						Logger.Log.Errorf("ParseCommitmentToV2ByBytes got error %v ", err)
						return nil, nil, err
					}

					temp, err := new(operation.Point).FromBytesS(commitmentBytesV2)
					if err != nil {
						Logger.Log.Errorf("commitmentBytesV2 is not byte operation.point %v ", err)
						return nil, nil, err
					}
					sumInputs.Add(sumInputs, temp)
					break
				}
			}
		}
		row = append(row, sumInputs.Sub(sumInputs, outputCommitments))
		ring[i] = row
		indexes[i] = rowIndexes
	}
	mlsagring := mlsag.NewRing(ring)
	return mlsagring, indexes, nil
}

func createPrivKeyMlsag(inp *[]*coin.InputCoin, out *[]*coin.OutputCoin, senderSK *key.PrivateKey) *[]*operation.Scalar {
	inputCoins := *inp
	outputCoins := *out

	sumRand := new(operation.Scalar).FromUint64(0)
	for _, in := range inputCoins {
		sumRand.Add(sumRand, in.CoinDetails.GetRandomness())
	}
	for _, out := range outputCoins {
		sumRand.Sub(sumRand, out.CoinDetails.GetRandomness())
	}

	sk := new(operation.Scalar).FromBytesS(*senderSK)
	privKeyMlsag := make([]*operation.Scalar, len(inputCoins)+1)
	for i := 0; i < len(inputCoins); i += 1 {
		privKeyMlsag[i] = sk
	}
	privKeyMlsag[len(inputCoins)] = sumRand
	return &privKeyMlsag
}

// signTx - signs tx
func signTxVer2(inp *[]*coin.InputCoin, out *[]*coin.OutputCoin, tx *Tx, params *TxPrivacyInitParams) error {
	if tx.Sig != nil {
		return NewTransactionErr(UnexpectedError, errors.New("input transaction must be an unsigned one"))
	}

	ringSize := privacy.RingSize
	if !params.hasPrivacy {
		ringSize = 1
	}

	var pi int = common.RandIntInterval(0, ringSize-1)
	shardID := common.GetShardIDFromLastByte(tx.PubKeyLastByteSender)

	ring, indexes, err := generateMlsagRingWithIndexes(inp, out, params, pi, shardID, ringSize)
	if err != nil {
		Logger.Log.Errorf("generateMlsagRingWithIndexes got error %v ", err)
		return err
	}
	privKeysMlsag := *createPrivKeyMlsag(inp, out, params.senderSK)
	keyImages := mlsag.ParseKeyImages(privKeysMlsag)
	for i := 0; i < len(tx.Proof.GetInputCoins()); i += 1 {
		tx.Proof.GetInputCoins()[i].CoinDetails.SetSerialNumber(keyImages[i])
	}

	sag := mlsag.NewMlsag(privKeysMlsag, ring, pi)

	tx.sigPrivKey, err = privacy.ArrayScalarToBytes(&privKeysMlsag)
	if err != nil {
		Logger.Log.Errorf("tx.SigPrivKey cannot parse arrayScalar to Bytes, error %v ", err)
		return err
	}

	txSigPubKey := new(TxSignatureVer2)
	txSigPubKey.SetIndexes(indexes)
	txSigPubKey.SetRing(ring)
	tx.SigPubKey, err = txSigPubKey.ToBytes()
	if err != nil {
		Logger.Log.Errorf("tx.SigPubKey cannot parse from Bytes, error %v ", err)
		return err
	}

	message := tx.Proof.Bytes()
	mlsagSignature, err := sag.Sign(message)
	if err != nil {
		Logger.Log.Errorf("Cannot sign mlsagSignature, error %v ", err)
		return err
	}

	tx.Sig, err = mlsagSignature.ToBytes()
	return err
}

func (*TxVersion2) Prove(tx *Tx, params *TxPrivacyInitParams) error {
	outputCoins, err := parseOutputCoins(params)
	if err != nil {
		Logger.Log.Errorf("Cannot parse outputcoin, error %v ", err)
		return err
	}
	for i := 0; i < len(*outputCoins); i += 1 {
		(*outputCoins)[i].CoinDetails.SetRandomness(operation.RandomScalar())
		(*outputCoins)[i].CoinDetails.SetCoinCommitment(
			coin.ParseCommitmentToV2WithCoin((*outputCoins)[i].CoinDetails),
		)
	}
	inputCoins := &params.inputCoins

	tx.Proof, err = privacy_v2.Prove(inputCoins, outputCoins, params.hasPrivacy, &params.paymentInfo)
	if err != nil {
		Logger.Log.Errorf("Error in privacy_v2.Prove, error %v ", err)
		return err
	}

	err = signTxVer2(inputCoins, outputCoins, tx, params)
	return err
}

func (txVer2 *TxVersion2) ProveASM(tx *Tx, params *TxPrivacyInitParamsForASM) error {
	return txVer2.Prove(tx, &params.txParam)
}

func checkRingIndexesWithDatabase(tx *Tx, ring *mlsag.Ring, indexes [][]*big.Int, transactionStateDB *statedb.StateDB, shardID byte, tokenID *common.Hash, isNewTransaction bool) (bool, error) {
	K := ring.GetKeys()
	n := len(K)
	m := len(K[0])

	if len(indexes) != n {
		return false, errors.New("Size row of indexes is not same with mlsagRing error")
	}
	if len(indexes[0]) != m-1 {
		return false, errors.New("Size column of indexes is not right error")
	}

	// This is txver2 so outputCoin should be in txver2 format
	outputCoins := tx.Proof.GetOutputCoins()
	outputCommitments := new(operation.Point).Identity()
	for i := 0; i < len(outputCoins); i += 1 {
		commitment := outputCoins[i].CoinDetails.GetCoinCommitment()
		outputCommitments.Add(outputCommitments, commitment)
	}
	feeCommitment := new(operation.Point).ScalarMult(
		operation.PedCom.G[operation.PedersenValueIndex],
		new(operation.Scalar).FromUint64(tx.Fee),
	)

	for i := 0; i < n; i += 1 {
		sumCommitment := new(operation.Point).Identity()
		sumCommitment.Sub(sumCommitment, feeCommitment)
		for j := 0; j < m-1; j += 1 {
			ringPubkey := K[i][j]
			index := indexes[i][j]

			ok, err := statedb.HasCommitmentIndex(transactionStateDB, *tokenID, index.Uint64(), shardID)
			if !ok || err != nil {
				Logger.Log.Errorf("Has commitment index error %v ", err)
				return false, err
			}
			commitmentByte, publicKeyByte, sndByte, err := statedb.GetCommitmentPublicKeyAddditionalByIndex(transactionStateDB, *tokenID, index.Uint64(), shardID)
			if err != nil {
				Logger.Log.Errorf("Get Commitment, PublicKey, Additional by index error %v ", err)
				return false, err
			}
			ringPubkeyBytes := ringPubkey.ToBytesS()
			if !bytes.Equal(ringPubkeyBytes, publicKeyByte) {
				return false, errors.New("Ring byte is not the same with database")
			}

			commitmentV2Byte, err := coin.ParseCommitmentToV2ByBytes(
				commitmentByte,
				publicKeyByte,
				sndByte,
				shardID,
			)
			if err != nil {
				Logger.Log.Errorf("Parsing to commitmentv2 by bytes error %v ", err)
				return false, err
			}
			commitmentV2, err := new(operation.Point).FromBytesS(commitmentV2Byte)
			if err != nil {
				Logger.Log.Errorf("CommitmentPoint from Byte got error %v ", err)
				return false, err
			}
			sumCommitment.Add(sumCommitment, commitmentV2)
		}
		sumCommitment.Sub(sumCommitment, outputCommitments)
		byteCommitment := sumCommitment.ToBytesS()
		byteRing := K[i][m-1].ToBytesS()
		if !bytes.Equal(byteCommitment, byteRing) {
			fmt.Println(byteCommitment)
			fmt.Println(byteRing)
			return false, errors.New("Ring byte is not the same with commitment sum")
		}
	}

	// Why???
	if isNewTransaction {
		for i := 0; i < len(outputCoins); i++ {
			// Check output coins' SND is not exists in SND list (Database)
			if ok, err := CheckSNDerivatorExistence(tokenID, outputCoins[i].CoinDetails.GetSNDerivator(), transactionStateDB); ok || err != nil {
				if err != nil {
					Logger.Log.Error(err)
				}
				Logger.Log.Errorf("snd existed: %d\n", i)
				return false, NewTransactionErr(SndExistedError, err, fmt.Sprintf("snd existed: %d\n", i))
			}
		}
	}

	return true, nil
}

// verifySigTx - verify signature on tx
func verifySigTxVer2(tx *Tx) (bool, error) {
	// check input transaction
	if tx.Sig == nil || tx.SigPubKey == nil {
		return false, NewTransactionErr(UnexpectedError, errors.New("input transaction must be an signed one"))
	}
	var err error

	txSigPubKey := new(TxSignatureVer2)
	err = txSigPubKey.FromBytes(tx.SigPubKey)
	if err != nil {
		return false, err
	}
	ring := txSigPubKey.GetRing()

	txSig, err := new(mlsag.MlsagSig).FromBytes(tx.Sig)
	if err != nil {
		return false, err
	}

	message := tx.Proof.Bytes()
	return mlsag.Verify(txSig, ring, message)
}

// TODO privacy
func (*TxVersion2) Verify(tx *Tx, hasPrivacy bool, transactionStateDB *statedb.StateDB, bridgeStateDB *statedb.StateDB, shardID byte, tokenID *common.Hash, isBatch bool, isNewTransaction bool) (bool, error) {
	var valid bool
	var err error

	if valid, err := verifySigTxVer2(tx); !valid {
		if err != nil {
			Logger.Log.Errorf("Error verifying signature ver2 with tx hash %s: %+v \n", tx.Hash().String(), err)
			return false, NewTransactionErr(VerifyTxSigFailError, err)
		}
		Logger.Log.Errorf("FAILED VERIFICATION SIGNATURE ver2 with tx hash %s", tx.Hash().String())
		return false, NewTransactionErr(VerifyTxSigFailError, fmt.Errorf("FAILED VERIFICATION SIGNATURE ver2 with tx hash %s", tx.Hash().String()))
	}

	if tx.Proof == nil {
		return true, nil
	}
	tokenID, err = parseTokenID(tokenID)
	if err != nil {
		return false, err
	}

	txSigPubKey := new(TxSignatureVer2)
	_ = txSigPubKey.FromBytes(tx.SigPubKey)
	ring := txSigPubKey.GetRing()
	indexes := txSigPubKey.GetIndexes()
	valid, err = checkRingIndexesWithDatabase(tx, ring, indexes, transactionStateDB, shardID, tokenID, isNewTransaction)
	if err != nil {
		Logger.Log.Errorf("Check ring indexes with database got error %v", err)
		return false, err
	}
	if !valid {
		return false, nil
	}

	// Verify the payment proof
	var txProofV2 *privacy.ProofV2 = tx.Proof.(*privacy.ProofV2)
	valid, err = txProofV2.Verify(hasPrivacy, tx.SigPubKey, tx.Fee, shardID, tokenID, isBatch, nil)

	if !valid {
		if err != nil {
			Logger.Log.Error(err)
		}
		Logger.Log.Error("FAILED VERIFICATION PAYMENT PROOF VER 2")
		err1, ok := err.(*privacy.PrivacyError)
		if ok {
			// parse error detail
			if err1.Code == privacy.ErrCodeMessage[errhandler.VerifyOneOutOfManyProofFailedErr].Code {
				if isNewTransaction {
					return false, NewTransactionErr(VerifyOneOutOfManyProofFailedErr, err1, tx.Hash().String())
				} else {
					// for old txs which be get from sync block or validate new block
					if tx.LockTime <= ValidateTimeForOneoutOfManyProof {
						// only verify by sign on block because of issue #504(that mean we should pass old tx, which happen before this issue)
						return true, nil
					} else {
						return false, NewTransactionErr(VerifyOneOutOfManyProofFailedErr, err1, tx.Hash().String())
					}
				}
			}
		}
		return false, NewTransactionErr(TxProofVerifyFailError, err, tx.Hash().String())
	}
	Logger.Log.Debugf("SUCCESSED VERIFICATION PAYMENT PROOF ")
	return true, nil
}
