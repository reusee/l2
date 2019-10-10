package l2

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"net"
)

type Outbound struct {
	Eth          []byte
	DestIP       *net.IP
	DestAddr     *net.HardwareAddr
	Serial       uint64
	PreferFormat WireFormat
}

type WireFormat uint8

const (
	FormatAESGCM WireFormat = iota

	AESGCMNonceSize = 12
)

type Inbound struct {
	Eth      []byte
	Serial   uint64
	DestAddr *net.HardwareAddr
}

func (n *Network) writeOutbound(w io.Writer, outbound *Outbound) error {
	plaintextBS := make([]byte,
		2+ // payload len
			len(outbound.Eth)+ // payload
			8, // serial
	)
	buf := bytes.NewBuffer(plaintextBS[:0])
	if err := binary.Write(buf, binary.LittleEndian, uint16(len(outbound.Eth))); err != nil {
		return err
	}
	_, err := buf.Write(outbound.Eth)
	if err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, outbound.Serial); err != nil {
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

func (n *Network) readInbound(r io.Reader) (inbound Inbound, err error) {
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

	r = bytes.NewReader(plaintext)
	if err = binary.Read(r, binary.LittleEndian, &l); err != nil {
		return
	}
	bs := make([]byte, int(l))
	if _, err = io.ReadFull(r, bs); err != nil {
		return
	}
	inbound.Eth = bs
	if err = binary.Read(r, binary.LittleEndian, &inbound.Serial); err != nil {
		return
	}
	return
}
