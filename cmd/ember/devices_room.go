package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tarakanof/ember/internal/devices"
)

const (
	roomTempMinC = -40
	roomTempMaxC = 85
	roomECO2Min  = 400
	roomAirMax   = 65000
	roomAQIMin   = 1
	roomAQIMax   = 5
	roomLuxMax   = 125000
	roomHumidMax = 100
)

var (
	roomAirStates = []string{"normal", "warm_up", "start_up", "invalid"}

	errNoRoomReadings = errors.New("no room readings")
)

type roomParts struct {
	raw    map[string]json.RawMessage
	prefix string
	bad    *[]string
}

func roomValue[T any](p roomParts, key string, valid func(T) bool) *T {
	raw, ok := p.raw[key]
	if !ok || string(raw) == "null" {
		return nil
	}
	var v T
	if json.Unmarshal(raw, &v) != nil || !valid(v) {
		*p.bad = append(*p.bad, p.prefix+key)
		return nil
	}
	return &v
}

func roomRange[T int | float64](lo, hi T) func(T) bool {
	return func(v T) bool { return v >= lo && v <= hi }
}

func parseDeviceRoom(raw json.RawMessage) (*devices.Room, error) {
	var parts map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, errors.New("room must be an object")
	}
	var bad []string
	p := roomParts{raw: parts, bad: &bad}
	room := &devices.Room{
		TempC:   roomValue(p, "t_c", roomRange[float64](roomTempMinC, roomTempMaxC)),
		RH:      roomValue(p, "rh", roomRange[float64](0, roomHumidMax)),
		ECO2:    roomValue(p, "eco2", roomRange(roomECO2Min, roomAirMax)),
		TVOC:    roomValue(p, "tvoc", roomRange(0, roomAirMax)),
		AQI:     roomValue(p, "aqi", roomRange(roomAQIMin, roomAQIMax)),
		Air:     roomValue(p, "air", func(s string) bool { return slices.Contains(roomAirStates, s) }),
		Lux:     roomValue(p, "lux", roomRange[float64](0, roomLuxMax)),
		Present: roomValue(p, "present", func(bool) bool { return true }),
		AgeS:    parseRoomAges(parts["age_s"], &bad),
	}
	if len(bad) > 0 {
		return room, errors.New(strings.Join(bad, ", "))
	}
	return room, nil
}

func parseRoomAges(raw json.RawMessage, bad *[]string) *devices.RoomAges {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var parts map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		*bad = append(*bad, "age_s")
		return nil
	}
	p := roomParts{raw: parts, prefix: "age_s.", bad: bad}
	valid := func(age int) bool { return age >= 0 }
	ages := devices.RoomAges{
		Climate:  roomValue(p, "climate", valid),
		Air:      roomValue(p, "air", valid),
		Light:    roomValue(p, "light", valid),
		Presence: roomValue(p, "presence", valid),
	}
	if ages == (devices.RoomAges{}) {
		return nil
	}
	return &ages
}

func (a *App) decodeDeviceRoom(r *http.Request, raw json.RawMessage) *devices.Room {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	room, err := parseDeviceRoom(raw)
	a.noteCheckinDrop(r, "room values", err, &a.roomDrops)
	return room
}

type deviceRoomReply struct {
	Room         *devices.Room `json:"room"`
	ReceivedAgeS int64         `json:"received_age_s"`
}

func (a *App) handleDeviceRoom(w http.ResponseWriter, r *http.Request) {
	room, age, err := a.devices.Room(r.PathValue("id"))
	if err != nil {
		a.writeDeviceError(w, r, err)
		return
	}
	if room == nil {
		writeError(w, http.StatusNotFound, errNoRoomReadings)
		return
	}
	room.ReceivedAt = room.ReceivedAt.UTC().Truncate(time.Second)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, deviceRoomReply{Room: room, ReceivedAgeS: int64(age / time.Second)})
}
