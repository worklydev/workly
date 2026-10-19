package api

import (
	"errors"

	"github.com/worklydev/workly/internal/store"
)

// QueueSettings, ScheduleSettings and ValidateName validate settings as the
// PUT handlers do, and apply the same defaults, so that `workly validate`
// and `workly plan` can check a workly.yaml and compare it without a server.
// Errors carry the message alone, without the error code.

func QueueSettings(in QueueConfig) (store.QueueConfig, error) {
	cfg, err := queueConfig(in)
	return cfg, message(err)
}

func ScheduleSettings(in ScheduleConfig, maxBody int64) (store.ScheduleConfig, error) {
	cfg, _, _, err := scheduleConfig(in, maxBody)
	return cfg, message(err)
}

func ValidateName(kind, name string) error { return message(validateName(kind, name)) }

func message(err error) error {
	if e, ok := err.(*apiError); ok {
		return errors.New(e.message)
	}
	return err
}
