package apns

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand"
	"strconv"
	"time"
)

// Push commands always start with command value 2.
const pushCommandValue = 2

// Your total notification payload cannot exceed 2 KB.
const MaxPayloadSizeBytes = 2048

// Every push notification gets a pseudo-unique identifier;
// this establishes the upper boundary for it. Apple will return
// this identifier if there is an issue sending your notification.
const IdentifierUbound = 9999

// Constants related to the payload fields and their lengths.
const (
	deviceTokenItemid            = 1
	payloadItemid                = 2
	notificationIdentifierItemid = 3
	expirationDateItemid         = 4
	priorityItemid               = 5
	deviceTokenLength            = 32
	notificationIdentifierLength = 4
	expirationDateLength         = 4
	priorityLength               = 1
)

// Payload contains the notification data for your request.
//
// Alert is an interface here because it supports either a string
// or a dictionary, represented within by an AlertDictionary struct.
type Payload struct {
	Alert            interface{} `json:"alert,omitempty"`
	Badge            int         `json:"badge,omitempty"`
	Sound            string      `json:"sound,omitempty"`
	ContentAvailable int         `json:"content-available,omitempty"`
	Category         string      `json:"category,omitempty"`
}

// NewPayload creates and returns a Payload structure.
func NewPayload() *Payload {
	return new(Payload)
}

// AlertDictionary is a more complex notification payload.
//
// From the APN docs: "Use the ... alert dictionary in general only if you absolutely need to."
// The AlertDictionary is suitable for specific localization needs.
type AlertDictionary struct {
	Body         string   `json:"body,omitempty"`
	ActionLocKey string   `json:"action-loc-key,omitempty"`
	LocKey       string   `json:"loc-key,omitempty"`
	LocArgs      []string `json:"loc-args,omitempty"`
	LaunchImage  string   `json:"launch-image,omitempty"`
}

// NewAlertDictionary creates and returns an AlertDictionary structure.
func NewAlertDictionary() *AlertDictionary {
	return new(AlertDictionary)
}

// PushNotification is the wrapper for the Payload.
// The length fields are computed in ToBytes() and aren't represented here.
type PushNotification struct {
	Identifier  uint32
	Expiry      uint32
	DeviceToken string
	Payload     map[string]interface{}
	Priority    uint8
}

// NewPushNotification creates and returns a PushNotification structure.
// It also initializes the pseudo-random identifier.
func NewPushNotification() (pn *PushNotification) {
	pn = new(PushNotification)
	pn.Payload = make(map[string]interface{})
	pn.Identifier = rand.New(rand.NewSource(time.Now().UnixNano())).Uint32()
	pn.Priority = 10
	return
}

// AddPayload sets the "aps" Payload section of the request. It also
// has a hack described within to deal with specific zero values.
func (pn *PushNotification) AddPayload(p *Payload) {
	// This deserves some explanation.
	//
	// Setting an exported field of type int to 0
	// triggers the omitempty behavior if you've set it.
	// Since the badge is optional, we should omit it if
	// it's not set. However, we want to include it if the
	// value is 0, so there's a hack in push_notification.go
	// that exploits the fact that Apple treats -1 for a
	// badge value as though it were 0 (i.e. it clears the
	// badge but doesn't stop the notification from going
	// through successfully.)
	//
	// Still a hack though :)
	if p.Badge == 0 {
		p.Badge = -1
	}
	pn.Set("aps", p)
}

// Get returns the value of a Payload key, if it exists.
func (pn *PushNotification) Get(key string) interface{} {
	return pn.Payload[key]
}

// Set defines the value of a Payload key.
func (pn *PushNotification) Set(key string, value interface{}) {
	pn.Payload[key] = value
}

// PayloadJSON returns the current Payload in JSON format.
func (pn *PushNotification) PayloadJSON() ([]byte, error) {
	return json.Marshal(pn.Payload)
}

// PayloadString returns the current Payload in string format.
func (pn *PushNotification) PayloadString() (string, error) {
	j, err := pn.PayloadJSON()
	return string(j), err
}

// ToBytes returns a byte array of the complete PushNotification
// struct. This array is what should be transmitted to the APN Service.
func (pn *PushNotification) ToBytes() ([]byte, error) {
	tokenHex, err := hex.DecodeString(pn.DeviceToken)
	if err != nil {
		return nil, err
	}
	if len(tokenHex) != deviceTokenLength {
		return nil, errors.New(fmt.Sprintf("device token has incorrect length %d was supposed to be %d. TOKEN IS %+v BUT ORIGINAL IS %+v", len(tokenHex), deviceTokenLength, tokenHex, pn.DeviceToken))
	}
	payload, err := pn.PayloadJSON()
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxPayloadSizeBytes {
		return nil, errors.New("payload is larger than the " + strconv.Itoa(MaxPayloadSizeBytes) + " byte limit")
	}

	frameBuffer := new(bytes.Buffer)
	binary.Write(frameBuffer, binary.BigEndian, uint8(deviceTokenItemid))
	binary.Write(frameBuffer, binary.BigEndian, uint16(deviceTokenLength))
	binary.Write(frameBuffer, binary.BigEndian, tokenHex)
	binary.Write(frameBuffer, binary.BigEndian, uint8(payloadItemid))
	binary.Write(frameBuffer, binary.BigEndian, uint16(len(payload)))
	binary.Write(frameBuffer, binary.BigEndian, payload)
	binary.Write(frameBuffer, binary.BigEndian, uint8(notificationIdentifierItemid))
	binary.Write(frameBuffer, binary.BigEndian, uint16(notificationIdentifierLength))
	binary.Write(frameBuffer, binary.BigEndian, pn.Identifier)
	binary.Write(frameBuffer, binary.BigEndian, uint8(expirationDateItemid))
	binary.Write(frameBuffer, binary.BigEndian, uint16(expirationDateLength))
	binary.Write(frameBuffer, binary.BigEndian, pn.Expiry)
	binary.Write(frameBuffer, binary.BigEndian, uint8(priorityItemid))
	binary.Write(frameBuffer, binary.BigEndian, uint16(priorityLength))
	binary.Write(frameBuffer, binary.BigEndian, pn.Priority)

	buffer := bytes.NewBuffer([]byte{})
	binary.Write(buffer, binary.BigEndian, uint8(pushCommandValue))
	binary.Write(buffer, binary.BigEndian, uint32(frameBuffer.Len()))
	binary.Write(buffer, binary.BigEndian, frameBuffer.Bytes())
	return buffer.Bytes(), nil
}
