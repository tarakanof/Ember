package main

import (
	"errors"
	"fmt"

	"github.com/tarakanof/ember/internal/devices"
)

const (
	devicesEpochHeader  = "X-Ember-Devices-Epoch"
	deviceConfigVersion = "X-Ember-Config-Version"
)

func (a *App) loadRegistries() error {
	moved, err := devices.MigrateClientRecords(a.devices.KV())
	if err != nil {
		err = fmt.Errorf("move client tokens out of the device registry: %w", err)
		a.devices.Fail(err)
		a.clients.Fail(err)
		return err
	}
	if moved > 0 {
		a.logger.Info("client tokens moved out of the device registry", "count", moved)
	}
	return errors.Join(a.devices.Load(), a.clients.Load())
}
