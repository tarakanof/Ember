package devices

import "time"

type Room struct {
	TempC      *float64  `json:"t_c,omitempty"`
	RH         *float64  `json:"rh,omitempty"`
	ECO2       *int      `json:"eco2,omitempty"`
	TVOC       *int      `json:"tvoc,omitempty"`
	AQI        *int      `json:"aqi,omitempty"`
	Air        *string   `json:"air,omitempty"`
	Lux        *float64  `json:"lux,omitempty"`
	Present    *bool     `json:"present,omitempty"`
	AgeS       *RoomAges `json:"age_s,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
}

type RoomAges struct {
	Climate  *int `json:"climate,omitempty"`
	Air      *int `json:"air,omitempty"`
	Light    *int `json:"light,omitempty"`
	Presence *int `json:"presence,omitempty"`
}

func (r *Room) clone() *Room {
	if r == nil {
		return nil
	}
	c := *r
	c.TempC, c.RH, c.Lux = clonePtr(r.TempC), clonePtr(r.RH), clonePtr(r.Lux)
	c.ECO2, c.TVOC, c.AQI = clonePtr(r.ECO2), clonePtr(r.TVOC), clonePtr(r.AQI)
	c.Air, c.Present = clonePtr(r.Air), clonePtr(r.Present)
	if r.AgeS != nil {
		c.AgeS = &RoomAges{
			Climate:  clonePtr(r.AgeS.Climate),
			Air:      clonePtr(r.AgeS.Air),
			Light:    clonePtr(r.AgeS.Light),
			Presence: clonePtr(r.AgeS.Presence),
		}
	}
	return &c
}

func (r *Registry) Room(id string) (*Room, time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.state.FindKnob(id)
	if d == nil {
		return nil, 0, ErrNotFound
	}
	if d.LastCheckin == nil || d.LastCheckin.Room == nil {
		return nil, 0, nil
	}
	room := d.LastCheckin.Room.clone()
	return room, max(r.Now().Sub(room.ReceivedAt), 0), nil
}
