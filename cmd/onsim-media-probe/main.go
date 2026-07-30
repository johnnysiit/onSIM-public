package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/hraban/opus"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

type call struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

type snapshot struct {
	ActiveCall *call `json:"activeCall"`
}

type offer struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

func main() {
	base := flag.String("url", "https://127.0.0.1:9443", "onSIM base URL")
	number := flag.String("number", "", "number to dial")
	cookie := flag.String("session", "", "onsim_session cookie value")
	seconds := flag.Int("seconds", 12, "tone duration")
	flag.Parse()
	if *number == "" || *cookie == "" {
		fatal(errors.New("-number and -session are required"))
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // local diagnostic CA
	}}
	var dialed call
	if err := request(client, *base, *cookie, http.MethodPost, "/api/v1/calls",
		map[string]string{"number": *number}, &dialed); err != nil {
		fatal(err)
	}
	fmt.Printf("call=%s waiting for active\n", dialed.ID)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var state snapshot
		if err := request(client, *base, *cookie, http.MethodGet, "/api/v1/state", nil, &state); err != nil {
			fatal(err)
		}
		if state.ActiveCall == nil || state.ActiveCall.ID != dialed.ID {
			fatal(errors.New("call ended before media became active"))
		}
		if state.ActiveCall.State == "active" {
			if err := sendTone(client, *base, *cookie, dialed.ID, *seconds); err != nil {
				fatal(err)
			}
			fmt.Println("tone complete; leaving hangup to the remote party")
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	fatal(errors.New("call was not answered within 60 seconds"))
}

func sendTone(client *http.Client, base, cookie, callID string, seconds int) error {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	defer pc.Close()
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
	}, "probe-audio", "onsim-media-probe")
	if err != nil {
		return err
	}
	if _, err = pc.AddTrack(track); err != nil {
		return err
	}
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		go func() {
			for {
				if _, _, readErr := remote.ReadRTP(); readErr != nil {
					return
				}
			}
		}()
	})
	connected := make(chan struct{})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		fmt.Printf("webrtc=%s\n", state)
		if state == webrtc.PeerConnectionStateConnected {
			select {
			case <-connected:
			default:
				close(connected)
			}
		}
	})
	local, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(local); err != nil {
		return err
	}
	select {
	case <-gathered:
	case <-time.After(10 * time.Second):
		return errors.New("ICE gathering timeout")
	}
	var answer offer
	if err = request(client, base, cookie, http.MethodPost, "/api/v1/calls/"+callID+"/media",
		offer{SDP: pc.LocalDescription().SDP, Type: "offer"}, &answer); err != nil {
		return err
	}
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		return err
	}
	select {
	case <-connected:
	case <-time.After(10 * time.Second):
		return errors.New("WebRTC connection timeout")
	}
	encoder, err := opus.NewEncoder(48000, 1, opus.AppVoIP)
	if err != nil {
		return err
	}
	pcm := make([]int16, 960)
	packet := make([]byte, 4000)
	frames := seconds * 50
	for frame := 0; frame < frames; frame++ {
		for i := range pcm {
			t := float64(frame*len(pcm)+i) / 48000
			pcm[i] = int16(math.Sin(2*math.Pi*1000*t) * 9000)
		}
		n, encodeErr := encoder.Encode(pcm, packet)
		if encodeErr != nil {
			return encodeErr
		}
		if err = track.WriteSample(pionmedia.Sample{
			Data: append([]byte(nil), packet[:n]...), Duration: 20 * time.Millisecond,
		}); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func request(client *http.Client, base, cookie, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Cookie", "onsim_session="+cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("media-probe-%d", time.Now().UnixNano()))
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, res.StatusCode, raw)
	}
	if output != nil {
		return json.NewDecoder(res.Body).Decode(output)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "media probe:", err)
	os.Exit(1)
}
