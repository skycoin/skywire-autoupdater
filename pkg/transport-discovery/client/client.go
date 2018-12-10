package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/watercompany/skywire-services/pkg/transport-discovery/api"
	"github.com/watercompany/skywire-services/pkg/transport-discovery/store"
)

type Client struct {
	addr   string
	client http.Client
	key    cipher.PubKey
	sec    cipher.SecKey
}

// Creates
func New(addr string) *Client {
	// Sanitize addr
	if addr == "" {
		addr = "http://localhost"
	}

	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}

	if strings.HasSuffix(addr, "/") {
		addr = addr[:len(addr)-1]
	}

	return &Client{
		addr:   addr,
		client: http.Client{},
	}
}

// WithPubAndSecKey set a public key to the client.
// When keys are set, the client will sign request before submitting.
// The signature information is transmitted in the header using:
// * SW-Public: The specified public key
// * SW-Nonce:  The nonce for that public key
// * SW-Sig:    The signature of the payload + the nonce
func (c *Client) WithPubAndSecKey(key cipher.PubKey, sec cipher.SecKey) *Client {
	c.key = key
	c.sec = sec
	return c
}

// Post POST a resource
func (c *Client) Post(ctx context.Context, path string, payload interface{}) (*http.Response, error) {
	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(payload); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.addr+path, body)
	if err != nil {
		return nil, err
	}

	return c.Do(req.WithContext(ctx))
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c.key.Null() {
		return c.client.Do(req)
	}

	req.Header.Add("SW-Public", c.key.Hex())
	nonce, err := c.getNextNonce(req.Context(), c.key)
	if err != nil {
		return nil, err
	}
	req.Header.Add("SW-Nonce", strconv.FormatUint(uint64(nonce), 10))

	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	req.Body = ioutil.NopCloser(bytes.NewBuffer(body))

	hash := cipher.SumSHA256([]byte(
		fmt.Sprintf("%s%d", string(body), nonce),
	))

	sig, err := cipher.SignHash(hash, c.sec)
	if err != nil {
		return nil, err
	}
	req.Header.Add("SW-Sig", sig.Hex())

	return c.client.Do(req)
}

func (c *Client) getNextNonce(ctx context.Context, key cipher.PubKey) (store.Nonce, error) {
	resp, err := c.client.Get(c.addr + "/incrementing-nonces/" + key.Hex())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("error getting current nonce: status: %d <- %v", resp.StatusCode, extractError(resp.Body))
	}

	var nr api.NonceResponse
	if err := json.NewDecoder(resp.Body).Decode(&nr); err != nil {
		return 0, err
	}

	return store.Nonce(nr.NextNonce), nil
}

func (c *Client) RegisterTransport(ctx context.Context, t *store.Transport) error {
	resp, err := c.Post(ctx, "/register", t)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusCreated {
		return nil
	}

	return fmt.Errorf("status: %d, error: %v", resp.StatusCode, extractError(resp.Body))
}

func (c *Client) DeregisterTransport(ctx context.Context, id store.ID) error {
	return nil
}

// extractError returns the decoded error message from Body.
func extractError(r io.Reader) error {
	var apiError api.Error

	body, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, &apiError); err != nil {
		return errors.New(string(body))
	}

	return errors.New(apiError.Error)
}
