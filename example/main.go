package main

import "time"
import "fmt"
import "io/ioutil"

import "github.com/topfreegames/apns-go"

func main() {

	payload := apns.NewPayload()
	payload.Alert = "Hello, world!"
	payload.Badge = 1
	
	pn := apns.NewPushNotification()
	pn.DeviceToken = "a023652ce07d37cc8f5c9edce07ba4acfbc9f7d0e4499b06ee0859422186c367"
	pn.AddPayload(payload)
	cx := map[string]int{"ab": 2}
	pn.Set("m", cx)
	tb, _ := pn.PayloadString()
	fmt.Printf("%s\n", string(tb))
	keyf, _ := ioutil.ReadFile("rsa_decrypted_key.key")
	rsacert, _ := ioutil.ReadFile("rsa_cert.cert")
	client := apns.BareClient("gateway.sandbox.push.apple.com:2195", rsacert, keyf)

	go func() {
		for response := range client.GetResponses() {
			fmt.Printf("message is %s\n", string(response))
		}
	}()
	
	beg := time.Now()
	for i := 1; i <= 100; i += 1 {
		if i == 2 {
			beg = time.Now()
		}
		_ = client.SendAsync(pn)
	}
	fmt.Println(time.Now().Sub(beg))
	time.Sleep(50 * time.Minute)
}
