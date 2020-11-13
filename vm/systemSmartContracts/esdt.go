//go:generate protoc -I=proto -I=$GOPATH/src -I=$GOPATH/src/github.com/ElrondNetwork/protobuf/protobuf  --gogoslick_out=. esdt.proto
package systemSmartContracts

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"sync"

	"github.com/ElrondNetwork/elrond-go/config"
	"github.com/ElrondNetwork/elrond-go/core"
	"github.com/ElrondNetwork/elrond-go/core/atomic"
	"github.com/ElrondNetwork/elrond-go/core/check"
	"github.com/ElrondNetwork/elrond-go/core/vmcommon"
	"github.com/ElrondNetwork/elrond-go/hashing"
	"github.com/ElrondNetwork/elrond-go/marshal"
	"github.com/ElrondNetwork/elrond-go/vm"
)

const minLengthForTokenName = 10
const maxLengthForTokenName = 20
const configKeyPrefix = "esdtConfig"
const allIssuedTokens = "allIssuedTokens"
const burnable = "canBurn"
const mintable = "canMint"
const canPause = "canPause"
const canFreeze = "canFreeze"
const canWipe = "canWipe"
const canChangeOwner = "canChangeOwner"
const upgradable = "canUpgrade"

const conversionBase = 10

type esdt struct {
	eei                 vm.SystemEI
	gasCost             vm.GasCost
	baseIssuingCost     *big.Int
	ownerAddress        []byte
	eSDTSCAddress       []byte
	endOfEpochSCAddress []byte
	marshalizer         marshal.Marshalizer
	hasher              hashing.Hasher
	enabledEpoch        uint32
	flagEnabled         atomic.Flag
	mutExecution        sync.RWMutex
}

// ArgsNewESDTSmartContract defines the arguments needed for the esdt contract
type ArgsNewESDTSmartContract struct {
	Eei                 vm.SystemEI
	GasCost             vm.GasCost
	ESDTSCConfig        config.ESDTSystemSCConfig
	ESDTSCAddress       []byte
	Marshalizer         marshal.Marshalizer
	Hasher              hashing.Hasher
	EpochNotifier       vm.EpochNotifier
	EndOfEpochSCAddress []byte
}

// NewESDTSmartContract creates the esdt smart contract, which controls the issuing of tokens
func NewESDTSmartContract(args ArgsNewESDTSmartContract) (*esdt, error) {
	if check.IfNil(args.Eei) {
		return nil, vm.ErrNilSystemEnvironmentInterface
	}
	if check.IfNil(args.Marshalizer) {
		return nil, vm.ErrNilMarshalizer
	}
	if check.IfNil(args.Hasher) {
		return nil, vm.ErrNilHasher
	}
	if check.IfNil(args.EpochNotifier) {
		return nil, vm.ErrNilEpochNotifier
	}

	baseIssuingCost, okConvert := big.NewInt(0).SetString(args.ESDTSCConfig.BaseIssuingCost, conversionBase)
	if !okConvert || baseIssuingCost.Cmp(big.NewInt(0)) < 0 {
		return nil, vm.ErrInvalidBaseIssuingCost
	}

	e := &esdt{
		eei:                 args.Eei,
		gasCost:             args.GasCost,
		baseIssuingCost:     baseIssuingCost,
		ownerAddress:        []byte(args.ESDTSCConfig.OwnerAddress),
		eSDTSCAddress:       args.ESDTSCAddress,
		hasher:              args.Hasher,
		marshalizer:         args.Marshalizer,
		enabledEpoch:        args.ESDTSCConfig.EnabledEpoch,
		endOfEpochSCAddress: args.EndOfEpochSCAddress,
	}
	args.EpochNotifier.RegisterNotifyHandler(e)

	return e, nil
}

// Execute calls one of the functions from the esdt smart contract and runs the code according to the input
func (e *esdt) Execute(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	e.mutExecution.RLock()
	defer e.mutExecution.RUnlock()

	if CheckIfNil(args) != nil {
		return vmcommon.UserError
	}

	if args.Function == core.SCDeployInitFunctionName {
		return e.init(args)
	}

	if !e.flagEnabled.IsSet() {
		e.eei.AddReturnMessage("ESDT SC disabled")
		return vmcommon.UserError
	}

	switch args.Function {
	case "issue":
		return e.issue(args)
	case "issueProtected":
		return e.issueProtected(args)
	case core.BuiltInFunctionESDTBurn:
		return e.burn(args)
	case "mint":
		return e.mint(args)
	case "freeze":
		return e.toggleFreeze(args, core.BuiltInFunctionESDTFreeze)
	case "unFreeze":
		return e.toggleFreeze(args, core.BuiltInFunctionESDTUnFreeze)
	case "wipe":
		return e.wipe(args)
	case "pause":
		return e.togglePause(args, core.BuiltInFunctionESDTPause)
	case "unPause":
		return e.togglePause(args, core.BuiltInFunctionESDTUnPause)
	case "claim":
		return e.claim(args)
	case "configChange":
		return e.configChange(args)
	case "esdtControlChanges":
		return e.esdtControlChanges(args)
	case "transferOwnership":
		return e.transferOwnership(args)
	case "getAllESDTTokens":
		return e.getAllESDTTokens(args)
	case "getTokenProperties":
		return e.getTokenProperties(args)
	}

	e.eei.AddReturnMessage("invalid method to call")
	return vmcommon.FunctionNotFound
}

func (e *esdt) init(_ *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	scConfig := &ESDTConfig{
		OwnerAddress:       e.ownerAddress,
		BaseIssuingCost:    e.baseIssuingCost,
		MinTokenNameLength: minLengthForTokenName,
		MaxTokenNameLength: maxLengthForTokenName,
	}
	err := e.saveESDTConfig(scConfig)
	if err != nil {
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) issueProtected(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if !bytes.Equal(args.CallerAddr, e.ownerAddress) {
		e.eei.AddReturnMessage("issueProtected can be called by whitelisted address only")
		return vmcommon.UserError
	}
	if len(args.Arguments) < 3 {
		e.eei.AddReturnMessage("not enough arguments")
		return vmcommon.FunctionWrongSignature
	}
	if len(args.Arguments[0]) != len(args.CallerAddr) {
		e.eei.AddReturnMessage("invalid owner address length")
		return vmcommon.FunctionWrongSignature
	}
	err := e.eei.UseGas(e.gasCost.MetaChainSystemSCsCost.ESDTIssue)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.OutOfGas
	}
	esdtConfig, err := e.getESDTConfig()
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}
	if args.CallValue.Cmp(esdtConfig.BaseIssuingCost) != 0 {
		e.eei.AddReturnMessage("callValue not equals with baseIssuingCost")
		return vmcommon.OutOfFunds
	}

	err = e.issueToken(args.Arguments[0], args.Arguments[1:])
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) issue(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if len(args.Arguments) < 2 {
		e.eei.AddReturnMessage("not enough arguments")
		return vmcommon.FunctionWrongSignature
	}
	err := e.eei.UseGas(e.gasCost.MetaChainSystemSCsCost.ESDTIssue)
	if err != nil {
		e.eei.AddReturnMessage("not enough gas")
		return vmcommon.OutOfGas
	}
	esdtConfig, err := e.getESDTConfig()
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}
	if len(args.Arguments[0]) < int(esdtConfig.MinTokenNameLength) ||
		len(args.Arguments[0]) > int(esdtConfig.MaxTokenNameLength) {
		e.eei.AddReturnMessage("token name length not in parameters")
		return vmcommon.FunctionWrongSignature
	}
	if args.CallValue.Cmp(esdtConfig.BaseIssuingCost) != 0 {
		e.eei.AddReturnMessage("callValue not equals with baseIssuingCost")
		return vmcommon.OutOfFunds
	}

	err = e.issueToken(args.CallerAddr, args.Arguments)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func isTokenNameHumanReadable(tokenName []byte) bool {
	for _, ch := range tokenName {
		isSmallCharacter := ch >= 'a' && ch <= 'z'
		isBigCharacter := ch >= 'A' && ch <= 'Z'
		isNumber := ch >= '0' && ch <= '9'
		isReadable := isSmallCharacter || isBigCharacter || isNumber
		if !isReadable {
			return false
		}
	}
	return true
}

func (e *esdt) issueToken(owner []byte, arguments [][]byte) error {
	tokenName := arguments[0]
	initialSupply := big.NewInt(0).SetBytes(arguments[1])
	if initialSupply.Cmp(big.NewInt(0)) <= 0 {
		return vm.ErrNegativeOrZeroInitialSupply
	}

	data := e.eei.GetStorage(tokenName)
	if len(data) > 0 {
		return vm.ErrTokenAlreadyRegistered
	}

	if !isTokenNameHumanReadable(tokenName) {
		return vm.ErrTokenNameNotHumanReadable
	}

	newESDTToken := &ESDTData{
		OwnerAddress: owner,
		TokenName:    tokenName,
		MintedValue:  initialSupply,
		BurntValue:   big.NewInt(0),
		Upgradable:   true,
	}
	err := upgradeProperties(newESDTToken, arguments[2:])
	if err != nil {
		return err
	}
	err = e.saveToken(newESDTToken)
	if err != nil {
		return err
	}

	esdtTransferData := core.BuiltInFunctionESDTTransfer + "@" + hex.EncodeToString(tokenName) + "@" + hex.EncodeToString(initialSupply.Bytes())
	err = e.eei.Transfer(owner, e.eSDTSCAddress, big.NewInt(0), []byte(esdtTransferData), 0)
	if err != nil {
		return err
	}

	e.addToIssuedTokens(string(tokenName))

	return nil
}

func upgradeProperties(token *ESDTData, args [][]byte) error {
	if len(args) == 0 {
		return nil
	}
	if len(args)%2 != 0 {
		return vm.ErrInvalidNumOfArguments
	}

	for i := 0; i < len(args); i += 2 {
		optionalArg := string(args[i])
		val, err := checkAndGetSetting(string(args[i+1]))
		if err != nil {
			return err
		}
		switch optionalArg {
		case burnable:
			token.Burnable = val
		case mintable:
			token.Mintable = val
		case canPause:
			token.CanPause = val
		case canFreeze:
			token.CanFreeze = val
		case canWipe:
			token.CanWipe = val
		case upgradable:
			token.Upgradable = val
		case canChangeOwner:
			token.CanChangeOwner = val
		default:
			return vm.ErrInvalidArgument
		}
	}

	return nil
}

func checkAndGetSetting(arg string) (bool, error) {
	if arg == "true" {
		return true, nil
	}
	if arg == "false" {
		return false, nil
	}
	return false, vm.ErrInvalidArgument
}

func getStringFromBool(val bool) string {
	if val {
		return "true"
	}
	return "false"
}

func (e *esdt) burn(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if len(args.Arguments) != 2 {
		e.eei.AddReturnMessage("number of arguments must be equal with 2")
		return vmcommon.FunctionWrongSignature
	}
	if args.CallValue.Cmp(zero) != 0 {
		e.eei.AddReturnMessage("callValue must be 0")
		return vmcommon.OutOfFunds
	}
	burntValue := big.NewInt(0).SetBytes(args.Arguments[1])
	if burntValue.Cmp(big.NewInt(0)) <= 0 {
		e.eei.AddReturnMessage("negative or 0 value to burn")
		return vmcommon.UserError
	}
	token, err := e.getExistingToken(args.Arguments[0])
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}
	if !token.Burnable {
		e.eei.AddReturnMessage("token is not burnable")
		return vmcommon.UserError
	}
	token.BurntValue.Add(token.BurntValue, burntValue)

	err = e.saveToken(token)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	err = e.eei.UseGas(args.GasProvided)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.OutOfGas
	}

	return vmcommon.Ok
}

func (e *esdt) mint(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if len(args.Arguments) < 2 || len(args.Arguments) > 3 {
		e.eei.AddReturnMessage("accepted arguments number 2/3")
		return vmcommon.FunctionWrongSignature
	}
	token, returnCode := e.basicOwnershipChecks(args)
	if returnCode != vmcommon.Ok {
		return returnCode
	}
	mintValue := big.NewInt(0).SetBytes(args.Arguments[1])
	if mintValue.Cmp(big.NewInt(0)) <= 0 {
		e.eei.AddReturnMessage("negative or zero mint value")
		return vmcommon.UserError
	}
	if !token.Mintable {
		e.eei.AddReturnMessage("token is not mintable")
		return vmcommon.UserError
	}

	token.MintedValue.Add(token.MintedValue, mintValue)
	err := e.saveToken(token)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	destination := token.OwnerAddress
	if len(args.Arguments) == 3 {
		if len(args.Arguments[2]) != len(args.CallerAddr) {
			e.eei.AddReturnMessage("destination address of invalid length")
			return vmcommon.UserError
		}
		destination = args.Arguments[2]
	}

	esdtTransferData := core.BuiltInFunctionESDTTransfer + "@" + hex.EncodeToString(token.TokenName) + "@" + hex.EncodeToString(mintValue.Bytes())
	err = e.eei.Transfer(destination, e.eSDTSCAddress, big.NewInt(0), []byte(esdtTransferData), 0)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) toggleFreeze(args *vmcommon.ContractCallInput, builtInFunc string) vmcommon.ReturnCode {
	if len(args.Arguments) != 2 {
		e.eei.AddReturnMessage("invalid number of arguments, wanted 2")
		return vmcommon.FunctionWrongSignature
	}
	token, returnCode := e.basicOwnershipChecks(args)
	if returnCode != vmcommon.Ok {
		return returnCode
	}
	if !token.CanFreeze {
		e.eei.AddReturnMessage("cannot freeze")
		return vmcommon.UserError
	}

	esdtTransferData := builtInFunc + "@" + hex.EncodeToString(token.TokenName)
	err := e.eei.Transfer(args.Arguments[1], e.eSDTSCAddress, big.NewInt(0), []byte(esdtTransferData), 0)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) wipe(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if len(args.Arguments) != 2 {
		e.eei.AddReturnMessage("invalid number of arguments, wanted 2")
		return vmcommon.FunctionWrongSignature
	}
	token, returnCode := e.basicOwnershipChecks(args)
	if returnCode != vmcommon.Ok {
		return returnCode
	}
	if !token.CanWipe {
		e.eei.AddReturnMessage("cannot wipe")
		return vmcommon.UserError
	}
	if len(args.Arguments[1]) != len(args.CallerAddr) {
		e.eei.AddReturnMessage("invalid arguments")
		return vmcommon.UserError
	}

	esdtTransferData := core.BuiltInFunctionESDTWipe + "@" + hex.EncodeToString(token.TokenName)
	err := e.eei.Transfer(args.Arguments[1], e.eSDTSCAddress, big.NewInt(0), []byte(esdtTransferData), 0)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) togglePause(args *vmcommon.ContractCallInput, builtInFunc string) vmcommon.ReturnCode {
	if len(args.Arguments) != 1 {
		e.eei.AddReturnMessage("invalid number of arguments, wanted 1")
		return vmcommon.FunctionWrongSignature
	}
	token, returnCode := e.basicOwnershipChecks(args)
	if returnCode != vmcommon.Ok {
		return returnCode
	}
	if !token.CanPause {
		e.eei.AddReturnMessage("cannot pause/un-pause")
		return vmcommon.UserError
	}
	if token.IsPaused && builtInFunc == core.BuiltInFunctionESDTPause {
		e.eei.AddReturnMessage("cannot pause an already paused contract")
		return vmcommon.UserError
	}
	if !token.IsPaused && builtInFunc == core.BuiltInFunctionESDTUnPause {
		e.eei.AddReturnMessage("cannot unPause an already un-paused contract")
		return vmcommon.UserError
	}

	token.IsPaused = !token.IsPaused
	err := e.saveToken(token)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	esdtTransferData := builtInFunc + "@" + hex.EncodeToString(token.TokenName)
	e.eei.SendGlobalSettingToAll(e.eSDTSCAddress, []byte(esdtTransferData))

	return vmcommon.Ok
}

func (e *esdt) configChange(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if !bytes.Equal(args.CallerAddr, e.ownerAddress) {
		e.eei.AddReturnMessage("configChange can be called by whitelisted address only")
		return vmcommon.UserError
	}
	if args.CallValue.Cmp(zero) != 0 {
		e.eei.AddReturnMessage("callValue must be 0")
		return vmcommon.UserError
	}
	err := e.eei.UseGas(e.gasCost.MetaChainSystemSCsCost.ESDTOperations)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.OutOfGas
	}
	if len(args.Arguments) != 4 {
		e.eei.AddReturnMessage(vm.ErrInvalidNumOfArguments.Error())
		return vmcommon.UserError
	}

	newConfig := &ESDTConfig{
		OwnerAddress:       args.Arguments[0],
		BaseIssuingCost:    big.NewInt(0).SetBytes(args.Arguments[1]),
		MinTokenNameLength: uint32(big.NewInt(0).SetBytes(args.Arguments[2]).Uint64()),
		MaxTokenNameLength: uint32(big.NewInt(0).SetBytes(args.Arguments[3]).Uint64()),
	}

	if len(newConfig.OwnerAddress) != len(args.RecipientAddr) {
		e.eei.AddReturnMessage("invalid arguments, first argument must be a valid address")
		return vmcommon.UserError
	}
	if newConfig.BaseIssuingCost.Cmp(zero) < 0 {
		e.eei.AddReturnMessage("invalid new base issueing cost")
		return vmcommon.UserError
	}
	if newConfig.MinTokenNameLength > newConfig.MaxTokenNameLength {
		e.eei.AddReturnMessage("invalid min and max token name lengths")
		return vmcommon.UserError
	}

	err = e.saveESDTConfig(newConfig)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) claim(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if !bytes.Equal(args.CallerAddr, e.ownerAddress) {
		e.eei.AddReturnMessage("claim can be called by whitelisted address only")
		return vmcommon.UserError
	}
	if args.CallValue.Cmp(zero) != 0 {
		e.eei.AddReturnMessage("callValue must be 0")
		return vmcommon.UserError
	}
	err := e.eei.UseGas(e.gasCost.MetaChainSystemSCsCost.ESDTOperations)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.OutOfGas
	}
	if len(args.Arguments) != 0 {
		e.eei.AddReturnMessage(vm.ErrInvalidNumOfArguments.Error())
		return vmcommon.UserError
	}

	scBalance := e.eei.GetBalance(args.RecipientAddr)
	err = e.eei.Transfer(args.CallerAddr, args.RecipientAddr, scBalance, nil, 0)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) getAllESDTTokens(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if args.CallValue.Cmp(zero) != 0 {
		e.eei.AddReturnMessage("callValue must be 0")
		return vmcommon.UserError
	}
	if len(args.Arguments) != 0 {
		e.eei.AddReturnMessage(vm.ErrInvalidNumOfArguments.Error())
		return vmcommon.UserError
	}
	err := e.eei.UseGas(e.gasCost.MetaChainSystemSCsCost.ESDTOperations)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.OutOfGas
	}

	savedData := e.eei.GetStorage([]byte(allIssuedTokens))
	err = e.eei.UseGas(e.gasCost.BaseOperationCost.DataCopyPerByte * uint64(len(savedData)))
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	e.eei.Finish(savedData)

	return vmcommon.Ok
}

func (e *esdt) getTokenProperties(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if args.CallValue.Cmp(zero) != 0 {
		e.eei.AddReturnMessage("callValue must be 0")
		return vmcommon.UserError
	}
	if len(args.Arguments) != 1 {
		e.eei.AddReturnMessage(vm.ErrInvalidNumOfArguments.Error())
		return vmcommon.UserError
	}
	err := e.eei.UseGas(e.gasCost.MetaChainSystemSCsCost.ESDTOperations)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.OutOfGas
	}

	esdtToken, err := e.getExistingToken(args.Arguments[0])
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	e.eei.Finish(esdtToken.TokenName)
	e.eei.Finish(esdtToken.OwnerAddress)
	e.eei.Finish([]byte(esdtToken.MintedValue.String()))
	e.eei.Finish([]byte(esdtToken.BurntValue.String()))
	e.eei.Finish([]byte("IsPaused-" + getStringFromBool(esdtToken.IsPaused)))
	e.eei.Finish([]byte("CanUpgrade-" + getStringFromBool(esdtToken.Upgradable)))
	e.eei.Finish([]byte("CanMint-" + getStringFromBool(esdtToken.Mintable)))
	e.eei.Finish([]byte("CanBurn-" + getStringFromBool(esdtToken.Burnable)))
	e.eei.Finish([]byte("CanChangeOwner-" + getStringFromBool(esdtToken.CanChangeOwner)))
	e.eei.Finish([]byte("CanPause-" + getStringFromBool(esdtToken.CanPause)))
	e.eei.Finish([]byte("CanFreeze-" + getStringFromBool(esdtToken.CanFreeze)))
	e.eei.Finish([]byte("CanWipe-" + getStringFromBool(esdtToken.CanWipe)))

	return vmcommon.Ok
}

func (e *esdt) addToIssuedTokens(newToken string) {
	allTokens := e.eei.GetStorage([]byte(allIssuedTokens))
	if len(allTokens) == 0 {
		e.eei.SetStorage([]byte(allIssuedTokens), []byte(newToken))
		return
	}

	allTokens = append(allTokens, []byte("@"+newToken)...)
	e.eei.SetStorage([]byte(allIssuedTokens), allTokens)
}

func (e *esdt) basicOwnershipChecks(args *vmcommon.ContractCallInput) (*ESDTData, vmcommon.ReturnCode) {
	if args.CallValue.Cmp(zero) != 0 {
		e.eei.AddReturnMessage("callValue must be 0")
		return nil, vmcommon.OutOfFunds
	}
	err := e.eei.UseGas(e.gasCost.MetaChainSystemSCsCost.ESDTOperations)
	if err != nil {
		e.eei.AddReturnMessage("not enough gas")
		return nil, vmcommon.OutOfGas
	}
	token, err := e.getExistingToken(args.Arguments[0])
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return nil, vmcommon.UserError
	}
	if !bytes.Equal(token.OwnerAddress, args.CallerAddr) {
		e.eei.AddReturnMessage("can be called by owner only")
		return nil, vmcommon.UserError
	}

	return token, vmcommon.Ok
}

func (e *esdt) transferOwnership(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if len(args.Arguments) != 2 {
		e.eei.AddReturnMessage("expected num of arguments 2")
		return vmcommon.FunctionWrongSignature
	}
	token, returnCode := e.basicOwnershipChecks(args)
	if returnCode != vmcommon.Ok {
		return returnCode
	}
	if !token.CanChangeOwner {
		e.eei.AddReturnMessage("cannot change owner of the token")
		return vmcommon.UserError
	}
	if len(args.Arguments[1]) != len(args.CallerAddr) {
		e.eei.AddReturnMessage("destination address of invalid length")
		return vmcommon.UserError
	}

	token.OwnerAddress = args.Arguments[1]
	err := e.saveToken(token)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) esdtControlChanges(args *vmcommon.ContractCallInput) vmcommon.ReturnCode {
	if len(args.Arguments) < 2 {
		e.eei.AddReturnMessage("not enough arguments")
		return vmcommon.FunctionWrongSignature
	}
	token, returnCode := e.basicOwnershipChecks(args)
	if returnCode != vmcommon.Ok {
		return returnCode
	}
	if !token.Upgradable {
		e.eei.AddReturnMessage("token is not upgradable")
		return vmcommon.UserError
	}

	err := upgradeProperties(token, args.Arguments[1:])
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}
	err = e.saveToken(token)
	if err != nil {
		e.eei.AddReturnMessage(err.Error())
		return vmcommon.UserError
	}

	return vmcommon.Ok
}

func (e *esdt) saveToken(token *ESDTData) error {
	marshaledData, err := e.marshalizer.Marshal(token)
	if err != nil {
		return err
	}

	e.eei.SetStorage(token.TokenName, marshaledData)
	return nil
}

func (e *esdt) getExistingToken(tokenName []byte) (*ESDTData, error) {
	marshalledData := e.eei.GetStorage(tokenName)
	if len(marshalledData) == 0 {
		return nil, vm.ErrNoTokenWithGivenName
	}

	token := &ESDTData{}
	err := e.marshalizer.Unmarshal(token, marshalledData)
	return token, err
}

func (e *esdt) getESDTConfig() (*ESDTConfig, error) {
	esdtConfig := &ESDTConfig{
		OwnerAddress:       e.ownerAddress,
		BaseIssuingCost:    e.baseIssuingCost,
		MinTokenNameLength: minLengthForTokenName,
		MaxTokenNameLength: maxLengthForTokenName,
	}
	marshalledData := e.eei.GetStorage([]byte(configKeyPrefix))
	if len(marshalledData) == 0 {
		return esdtConfig, nil
	}

	err := e.marshalizer.Unmarshal(esdtConfig, marshalledData)
	return esdtConfig, err
}

func (e *esdt) saveESDTConfig(esdtConfig *ESDTConfig) error {
	marshaledData, err := e.marshalizer.Marshal(esdtConfig)
	if err != nil {
		return err
	}

	e.eei.SetStorage([]byte(configKeyPrefix), marshaledData)
	return nil
}

// EpochConfirmed is called whenever a new epoch is confirmed
func (e *esdt) EpochConfirmed(epoch uint32) {
	e.flagEnabled.Toggle(epoch >= e.enabledEpoch)
	log.Debug("esdt contract", "enabled", e.flagEnabled.IsSet())
}

// SetNewGasCost is called whenever a gas cost was changed
func (e *esdt) SetNewGasCost(gasCost vm.GasCost) {
	e.mutExecution.Lock()
	e.gasCost = gasCost
	e.mutExecution.Unlock()
}

// IsInterfaceNil returns true if underlying object is nil
func (e *esdt) IsInterfaceNil() bool {
	return e == nil
}
