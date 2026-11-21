package adb

import (
	"context"
	"os/exec"
	"strings"

	"devorbit.net/agents/host-bridge/internal/domain"
)

type ADBClient struct {
	adbBinary string
}

func NewADBClient(adbBinary string) *ADBClient {
	if adbBinary == "" {
		adbBinary = "adb"
	}
	return &ADBClient{adbBinary: adbBinary}
}

func (a *ADBClient) ListDevices(ctx context.Context) ([]domain.ADBDevice, error) {
	cmd := exec.CommandContext(ctx, a.adbBinary, "devices", "-l")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(output), "\n")
	devices := []domain.ADBDevice{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices attached") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		serial := fields[0]
		stateStr := fields[1]

		var state domain.ADBDeviceState
		switch stateStr {
		case "device":
			state = domain.DeviceStateOnline
		case "unauthorized":
			state = domain.DeviceStateUnauthorized
		default:
			state = domain.DeviceStateOffline
		}

		model := ""
		product := ""
		for _, field := range fields[2:] {
			if strings.HasPrefix(field, "model:") {
				model = strings.TrimPrefix(field, "model:")
			}
			if strings.HasPrefix(field, "product:") {
				product = strings.TrimPrefix(field, "product:")
			}
		}

		devices = append(devices, domain.ADBDevice{
			Serial:  serial,
			State:   state,
			Model:   model,
			Product: product,
		})
	}

	return devices, nil
}
