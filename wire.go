package l2

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"math/rand"
	"net"
)

type WireData struct {
	Eth    []byte
	Serial uint64
}

type Outbound struct {
	WireData
	DestIP       *net.IP
	DestAddr     *net.HardwareAddr
	PreferFormat WireFormat
}

type WireFormat uint8

const (
	FormatAESGCM WireFormat = iota

	AESGCMNonceSize = 12
)

type Inbound struct {
	WireData
	DestAddr    *net.HardwareAddr
	BridgeIndex uint8
}

func (n *Network) writeOutbound(w io.Writer, outbound *Outbound) error {
	buf := new(bytes.Buffer)
	if err := gob.NewEncoder(buf).Encode(outbound.WireData); err != nil {
		return err
	}
	plaintext := buf.Bytes()
	switch outbound.PreferFormat {

	case FormatAESGCM:

		block, err := aes.NewCipher(n.CryptoKey)
		ce(err)
		aead, err := cipher.NewGCM(block)
		ce(err)
		nonce := make([]byte, AESGCMNonceSize)
		for i := 0; i+8 <= AESGCMNonceSize; i += 4 {
			binary.LittleEndian.PutUint64(nonce[i:i+8], rand.Uint64())
		}
		buf := make([]byte,
			1+ // format
				AESGCMNonceSize+ // nonce
				len(plaintext)+ // plaintext
				aead.Overhead(), // overhead
		)
		buf[0] = byte(FormatAESGCM)
		copy(
			buf[1:1+AESGCMNonceSize],
			nonce,
		)
		ciphertext := aead.Seal(
			buf[1+AESGCMNonceSize:1+AESGCMNonceSize],
			nonce,
			plaintext,
			nil,
		)
		buf = buf[:1+AESGCMNonceSize+len(ciphertext)]
		if err := binary.Write(w, binary.LittleEndian, uint16(len(buf))); err != nil {
			return err
		}
		if _, err := w.Write(buf); err != nil {
			return err
		}

	}

	return nil
}

var errBadFrame = fmt.Errorf("bad frame")

func (n *Network) readInbound(r io.Reader) (inbound *Inbound, err error) {
	var l uint16
	if err = binary.Read(r, binary.LittleEndian, &l); err != nil {
		return
	}
	ciphertextBs := make([]byte, int(l))
	if _, err = io.ReadFull(r, ciphertextBs); err != nil {
		return
	}
	ciphertext := ciphertextBs

	if len(ciphertext) == 0 {
		err = errBadFrame
		return
	}

	var plaintext []byte
	switch WireFormat(ciphertext[0]) {

	case FormatAESGCM:
		ciphertext = ciphertext[1:]
		if len(ciphertext) < AESGCMNonceSize {
			err = errBadFrame
			return
		}
		nonce := ciphertext[:AESGCMNonceSize]
		ciphertext = ciphertext[AESGCMNonceSize:]
		block, e := aes.NewCipher(n.CryptoKey)
		ce(e)
		aead, e := cipher.NewGCM(block)
		ce(e)
		plaintext, err = aead.Open(ciphertext[:0], nonce, ciphertext, nil)
		if err != nil {
			return
		}

	}

	if err = gob.NewDecoder(bytes.NewReader(plaintext)).Decode(&inbound); err != nil {
		pt("%v\n", err)
		return
	}
	return
}
