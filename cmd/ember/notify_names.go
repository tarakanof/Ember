package main

import (
	"errors"
	"net/http"

	"github.com/tarakanof/ember/internal/awtrix"
)

// Notification names. awtrix-ng lets a notification carry a `name`, which
// DELETE /api/v1/notifications/{name} then matches exactly — so Ember can
// retract its OWN popup instead of clearing whatever happens to be on screen.
// Every notification Ember pushes carries one of these; the queue holds 32, and
// names make each entry addressable.
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

// isAPINotFound reports whether err is the device answering 404 — for a
// dismiss-by-name that means the notification is already gone, which is the
// expected outcome when the firmware's own button handling cleared it first.
func isAPINotFound(err error) bool {
	var apiErr *awtrix.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}
