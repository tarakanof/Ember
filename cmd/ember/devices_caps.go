package main

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/tarakanof/ember/internal/devices"
)

const (
	capsErrorMaxLen = 160
)

const (
	featureWxFaces   = "wx_faces"
	featureNPCover   = "np_cover"
	featureWxMoments = "wx_moments"
	featureDemo      = "demo"
	featureUsage     = "usage"
	featureShown     = "shown"
	featureRoom      = "room"
)

func decodeCaps(raw json.RawMessage) (*devices.Caps, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, ""
	}
	var c devices.Caps
	err := json.Unmarshal(raw, &c)
	if err == nil {
		err = rejectNullRotations(raw)
	}
	if err == nil {
		err = c.Validate()
	}
	if err != nil {
		msg := err.Error()
		if len(msg) > capsErrorMaxLen {
			msg = strings.ToValidUTF8(msg[:capsErrorMaxLen], "")
		}
		return nil, msg
	}
	return c.Normalized(), ""
}

func rejectNullRotations(raw json.RawMessage) error {
	var probe struct {
		Rotations []json.RawMessage `json:"rotations"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return err
	}
	for _, r := range probe.Rotations {
		if string(r) == "null" {
			return errors.New("rotations must list integers, not null")
		}
	}
	return nil
}
