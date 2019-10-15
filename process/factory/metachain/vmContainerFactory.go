package metachain

import (
	"github.com/ElrondNetwork/elrond-go/data/state"
	"github.com/ElrondNetwork/elrond-go/process"
	"github.com/ElrondNetwork/elrond-go/process/factory"
	"github.com/ElrondNetwork/elrond-go/process/factory/containers"
	"github.com/ElrondNetwork/elrond-go/process/smartContract/hooks"
	systemVMFactory "github.com/ElrondNetwork/elrond-go/vm/factory"
	systemVMProcess "github.com/ElrondNetwork/elrond-go/vm/process"
	"github.com/ElrondNetwork/elrond-go/vm/systemSmartContracts"
	"github.com/ElrondNetwork/elrond-vm-common"
)

type vmContainerFactory struct {
	accounts         state.AccountsAdapter
	addressConverter state.AddressConverter
	vmAccountsDB     *hooks.VMAccountsDB
	cryptoHook       vmcommon.CryptoHook
}

// NewVMContainerFactory is responsible for creating a new virtual machine factory object
func NewVMContainerFactory(
	accounts state.AccountsAdapter,
	addressConverter state.AddressConverter,
) (*vmContainerFactory, error) {
	if accounts == nil || accounts.IsInterfaceNil() {
		return nil, process.ErrNilAccountsAdapter
	}
	if addressConverter == nil || addressConverter.IsInterfaceNil() {
		return nil, process.ErrNilAddressConverter
	}

	vmAccountsDB, err := hooks.NewVMAccountsDB(accounts, addressConverter)
	if err != nil {
		return nil, err
	}
	cryptoHook := hooks.NewVMCryptoHook()

	return &vmContainerFactory{
		accounts:         accounts,
		addressConverter: addressConverter,
		vmAccountsDB:     vmAccountsDB,
		cryptoHook:       cryptoHook,
	}, nil
}

// Create sets up all the needed virtual machine returning a container of all the VMs
func (vmf *vmContainerFactory) Create() (process.VirtualMachinesContainer, error) {
	container := containers.NewVirtualMachinesContainer()

	vm, err := vmf.createSystemVM()
	if err != nil {
		return nil, err
	}

	err = container.Add(factory.SystemVirtualMachine, vm)
	if err != nil {
		return nil, err
	}

	return container, nil
}

func (vmf *vmContainerFactory) createSystemVM() (vmcommon.VMExecutionHandler, error) {
	systemEI, err := systemSmartContracts.NewVMContext(vmf.vmAccountsDB, vmf.cryptoHook)
	if err != nil {
		return nil, err
	}

	scFactory, err := systemVMFactory.NewSystemSCFactory(systemEI)
	if err != nil {
		return nil, err
	}

	systemContracts, err := scFactory.Create()
	if err != nil {
		return nil, err
	}

	systemVM, err := systemVMProcess.NewSystemVM(systemEI, systemContracts, factory.SystemVirtualMachine)
	if err != nil {
		return nil, err
	}

	return systemVM, nil
}

// VMAccountsDB returns the created vmAccountsDB
func (vmf *vmContainerFactory) VMAccountsDB() *hooks.VMAccountsDB {
	return vmf.vmAccountsDB
}

// IsInterfaceNil returns true if there is no value under the interface
func (vmf *vmContainerFactory) IsInterfaceNil() bool {
	if vmf == nil {
		return true
	}
	return false
}