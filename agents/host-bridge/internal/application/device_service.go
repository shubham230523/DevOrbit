package application

import (
	"context"

	"devorbit.net/agents/host-bridge/internal/domain"
	"devorbit.net/agents/host-bridge/internal/infrastructure/adb"
)

type DeviceService struct {
	client *adb.ADBClient
}

func NewDeviceService(client *adb.ADBClient) *DeviceService {
	return &DeviceService{client: client}
}

func (s *DeviceService) ListADBDevices(ctx context.Context) ([]domain.ADBDevice, error) {
	return s.client.ListDevices(ctx)
}
