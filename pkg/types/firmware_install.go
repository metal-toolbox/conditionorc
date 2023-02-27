package types

import "github.com/pkg/errors"

// TODO: move into a shared package

var (
	ErrFirmwareInstallParameter = errors.New("invalid firmware install parameter")
)

// Firmware holds parameters for a firmware install and is part of FirmwareInstallOutofbandParameters.
type Firmware struct {
	Version       string `yaml:"version"`
	URL           string `yaml:"URL"`
	FileName      string `yaml:"filename"`
	Utility       string `yaml:"utility"`
	Model         string `yaml:"model"`
	Vendor        string `yaml:"vendor"`
	ComponentSlug string `yaml:"componentslug"`
	Checksum      string `yaml:"checksum"`
}

// firmwareInstallParameters define firmwareInstall condition parameters.
type FirmwareInstallOutofbandParameters struct {
	InventoryAfterUpdate bool        `json:"inventory_after_update,omitempty"`
	ForceInstall         bool        `json:"force_install,omitempty"`
	FirmwareSetID        string      `json:"firmwareSetID,omitempty"`
	FirmwareList         []*Firmware `json:"firmware_list,omitempty"`
}

// Validate fields implements the Parameters interface to validate FirmwareInstallOutofbandParameters.
func (f *FirmwareInstallOutofbandParameters) Validate() error {
	if f.FirmwareSetID == "" && len(f.FirmwareList) == 0 {
		return errors.Wrap(ErrFirmwareInstallParameter, "expected either a FirmwareSetID OR a FirmwareList")
	}

	if f.FirmwareSetID != "" && len(f.FirmwareList) > 0 {
		return errors.Wrap(ErrFirmwareInstallParameter, "expected either a FirmwareSetID OR a FirmwareList, not both")
	}

	return nil
}
