package main

import (
	"errors"
	"time"

	"github.com/tarakanof/ember/internal/weather"
)

var openMeteoModels = []string{weather.DefaultOpenMeteoModel, "ecmwf_ifs", "ecmwf_ifs025", "icon_seamless", "gfs_seamless", "ukmo_seamless"}

func statusOf(err error) int {
	var he *weather.HTTPError
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

func retryAfterOf(err error) time.Duration {
	var he *weather.HTTPError
	if errors.As(err, &he) {
		return he.RetryAfter
	}
	return 0
}
