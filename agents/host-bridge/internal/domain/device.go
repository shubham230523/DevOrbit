package domain

type ADBDeviceState string

const (
	DeviceStateOnline       ADBDeviceState = "device"
	DeviceStateUnauthorized ADBDeviceState = "unauthorized"
	DeviceStateOffline      ADBDeviceState = "offline"
)

type ADBDevice struct {
	Serial  string         `json:"serial"`
	State   ADBDeviceState `json:"state"`
	Model   string         `json:"model,omitempty"`
	Product string         `json:"product,omitempty"`
}
