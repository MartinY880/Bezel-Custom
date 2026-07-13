package system

// PackageUpdate describes a single available OS package update.
type PackageUpdate struct {
	Name             string `json:"n" cbor:"0,keyasint"`
	CurrentVersion   string `json:"cv,omitempty" cbor:"1,keyasint,omitempty"`
	AvailableVersion string `json:"av,omitempty" cbor:"2,keyasint,omitempty"`
	// Held marks packages pinned by the admin (apt-mark hold); they can't be
	// applied without lifting the hold, so the UI shows them unselectable.
	Held bool `json:"hd,omitempty" cbor:"3,keyasint,omitempty"`
}
