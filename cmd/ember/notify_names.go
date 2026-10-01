package main

import (
	"errors"
	"net/http"

	"github.com/tarakanof/ember/internal/awtrix"
)

const (
	notifyNameReminder     = "ember-reminder"
	notifyNameMeeting      = "ember-meeting"
	notifyNameWeatherPopup = "ember-weather-popup"
	notifyNameAirPopup     = "ember-air-popup"
	notifyNameSunPopup     = "ember-sun-popup"
	notifyNameUsageAlarm   = "ember-usage-alarm"
	notifyNamePomodoro     = "ember-pomodoro"
	notifyNameNotify       = "ember-notify"
)

func isAPINotFound(err error) bool {
	var apiErr *awtrix.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}
