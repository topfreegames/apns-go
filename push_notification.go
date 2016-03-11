package apns

import (
	"bytes"
	"encoding/binary"
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
	deviceTokenItemid            int8 = 1
	payloadItemid                int8 = 2
	notificationIdentifierItemid int8 = 3
	expirationDateItemid         int8 = 4
	priorityItemid               int8 = 5
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
	Identifier  uint32 `json:"identifier"`
	Expiry      uint32 `json:"expiry"`
	DeviceToken string `json:"device_token"`
	Payload     map[string]interface{} `json:"payload"`
	Priority    uint8 `json:"priority"`
}

// NewPushNotification creates and returns a PushNotification structure.
// It also initializes the pseudo-random identifier.
func NewPushNotification() (pn *PushNotification) {
	pn = new(PushNotification)
	pn.Payload = make(map[string]interface{})
	pn.Identifier = uint32(rand.New(rand.NewSource(time.Now().UnixNano())).Int31n(9999))
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
	token, err := hex.DecodeString(pn.DeviceToken)
	if err != nil {
		return nil, err
	}
	Payload, err := pn.PayloadJSON()
	if err != nil {
		return nil, err
	}
	if len(Payload) > MaxPayloadSizeBytes {
		return nil, errors.New("Payload is larger than the " + strconv.Itoa(MaxPayloadSizeBytes) + " byte limit")
	}

	tokenLen := len(token)
	payloadLen := len(Payload)
	identifierLen := 4
	expiryLen := 4
	priorityLen := 1
	
	frameBuffer := new(bytes.Buffer)

	binary.Write(frameBuffer, binary.BigEndian, deviceTokenItemid)
	binary.Write(frameBuffer, binary.BigEndian, int16(tokenLen))
	binary.Write(frameBuffer, binary.BigEndian, token)
	binary.Write(frameBuffer, binary.BigEndian, payloadItemid)
	binary.Write(frameBuffer, binary.BigEndian, int16(payloadLen))
	binary.Write(frameBuffer, binary.BigEndian, Payload)
	binary.Write(frameBuffer, binary.BigEndian, notificationIdentifierItemid)
	binary.Write(frameBuffer, binary.BigEndian, int16(identifierLen))
	binary.Write(frameBuffer, binary.BigEndian, int32(pn.Identifier))
	
	binary.Write(frameBuffer, binary.BigEndian, expirationDateItemid)
	binary.Write(frameBuffer, binary.BigEndian, int16(expiryLen))

	binary.Write(frameBuffer, binary.BigEndian, pn.Expiry)		

	
	binary.Write(frameBuffer, binary.BigEndian, priorityItemid)
	binary.Write(frameBuffer, binary.BigEndian, int16(priorityLen))
	binary.Write(frameBuffer, binary.BigEndian, int8(pn.Priority))

	buffer := bytes.NewBuffer([]byte{})
	binary.Write(buffer, binary.BigEndian, pushCommandValue)
	binary.Write(buffer, binary.BigEndian, frameBuffer.Len())
	binary.Write(buffer, binary.BigEndian, frameBuffer.Bytes())

	return buffer.Bytes(), nil
}
