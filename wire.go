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
	"sync"
	"unsafe"

	"github.com/golang/snappy"
	"github.com/reusee/sb"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/poly1305"
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

	encodeOnce sync.Once
	encoded    []byte
	err        error
}

type WireFormat byte

const (
	FormatChacha20Poly1305 WireFormat = iota
	FormatAESGCM
	FormatChacha20Poly1305Partial

	AESGCMNonceSize = 12
)

type Inbound struct {
	WireData
	DestAddr    *net.HardwareAddr
	BridgeIndex uint8
}

type WriteOutbound func(w io.Writer, outbound *Outbound) (err error)

func (Network) WriteOutbound(
	key CryptoKey,
	keyInt CryptoKeyInt,
) WriteOutbound {
	return func(w io.Writer, outbound *Outbound) (err error) {
		if err = outbound.encode(key, keyInt); err != nil {
			return
		}
		if err := binary.Write(w, binary.LittleEndian, uint16(len(outbound.encoded))); err != nil {
			return err
		}
		if _, err := w.Write(outbound.encoded); err != nil {
			return err
		}
		return nil
	}
}

func (outbound *Outbound) encode(key CryptoKey, keyInt CryptoKeyInt) (err error) {
	defer he(&err)
	outbound.encodeOnce.Do(func() {
		bs := new(bytes.Buffer)
		if err := sb.Copy(
			sb.Marshal(sb.Tuple{
				outbound.WireData.Eth,
				outbound.WireData.Serial,
			}),
			sb.Encode(bs),
		); err != nil {
			outbound.err = err
			return
		}
		plaintext := snappy.Encode(nil, bs.Bytes())

		var buf []byte
		switch outbound.PreferFormat {

		case FormatAESGCM:

			block, err := aes.NewCipher(key)
			ce(err)
			aead, err := cipher.NewGCM(block)
			ce(err)
			buf = make([]byte,
				1+ // format
					AESGCMNonceSize+ // nonce
					len(plaintext)+ // plaintext
					aead.Overhead(), // overhead
			)
			buf[0] = byte(FormatAESGCM)
			nonce := buf[1 : 1+AESGCMNonceSize]
			for i := 0; i+8 <= AESGCMNonceSize; i += 4 {
				binary.LittleEndian.PutUint64(nonce[i:i+8], rand.Uint64())
			}
			ciphertext := aead.Seal(
				buf[1+AESGCMNonceSize:1+AESGCMNonceSize],
				nonce,
				plaintext,
				nil,
			)
			buf = buf[:1+AESGCMNonceSize+len(ciphertext)]

		case FormatChacha20Poly1305:
			aead, err := chacha20poly1305.NewX(key)
			ce(err)
			buf = make([]byte,
				1+
					chacha20poly1305.NonceSizeX+
					len(plaintext)+
					aead.Overhead(),
			)
			// [1] FrameChacha20Poly1305
			// [chacha20poly1305.NonceSizeX] nonce
			// [v] ciphertext
			buf[0] = byte(FormatChacha20Poly1305)
			nonce := buf[1 : 1+chacha20poly1305.NonceSizeX]
			for i := 0; i+8 <= chacha20poly1305.NonceSizeX; i += 4 {
				binary.LittleEndian.PutUint64(nonce[i:i+8], rand.Uint64())
			}
			ciphertext := aead.Seal(
				buf[1+chacha20poly1305.NonceSizeX:1+chacha20poly1305.NonceSizeX],
				nonce,
				plaintext,
				nil,
			)
			buf = buf[:1+chacha20poly1305.NonceSizeX+len(ciphertext)]

		case FormatChacha20Poly1305Partial:
			// [1] FrameChacha20Poly1305Partial
			// [32] poly1305 key
			// [8] 42
			// [16] poly1305 sum
			// [v] payload
			buf = make([]byte, 1+32+8+16+len(plaintext))
			buf[0] = byte(FormatChacha20Poly1305Partial)
			binary.LittleEndian.PutUint64(buf[1:9], rand.Uint64())
			binary.LittleEndian.PutUint64(buf[9:17], rand.Uint64())
			binary.LittleEndian.PutUint64(buf[17:25], uint64(keyInt))
			binary.LittleEndian.PutUint64(buf[25:33], rand.Uint64())
			binary.LittleEndian.PutUint64(buf[33:41], 42)
			poly1305.Sum(
				(*[16]byte)(unsafe.Pointer(&buf[41])),
				buf[33:41],
				(*[32]byte)(unsafe.Pointer(&buf[1])),
			)
			binary.LittleEndian.PutUint64(buf[17:25], rand.Uint64())
			copy(buf[57:], plaintext)

		}
		outbound.encoded = buf
	})

	return outbound.err
}

var errBadFrame = fmt.Errorf("bad frame")

type ReadInbound func(r io.Reader) (inbound *Inbound, err error)

func (Network) ReadInbound(
	key CryptoKey,
	keyInt CryptoKeyInt,
) ReadInbound {
	return func(r io.Reader) (inbound *Inbound, err error) {

		defer he(&err)
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
			block, e := aes.NewCipher(key)
			ce(e)
			aead, e := cipher.NewGCM(block)
			ce(e)
			plaintext, err = aead.Open(ciphertext[:0], nonce, ciphertext, nil)
			if err != nil {
				return
			}

		case FormatChacha20Poly1305:
			ciphertext = ciphertext[1:]
			if len(ciphertext) < chacha20poly1305.NonceSizeX {
				err = errBadFrame
				return
			}
			nonce := ciphertext[:chacha20poly1305.NonceSizeX]
			ciphertext = ciphertext[chacha20poly1305.NonceSizeX:]
			aead, e := chacha20poly1305.NewX(key)
			ce(e)
			plaintext, err = aead.Open(ciphertext[:0], nonce, ciphertext, nil)
			if err != nil {
				err = errBadFrame
				return
			}

		case FormatChacha20Poly1305Partial:
			if len(ciphertext) < 1+32+16+8 {
				err = errBadFrame
				return
			}
			binary.LittleEndian.PutUint64(ciphertext[17:25], uint64(keyInt))
			key := (*[32]byte)(unsafe.Pointer(&ciphertext[1]))
			magic := ciphertext[33:41]
			sum := (*[16]byte)(unsafe.Pointer(&ciphertext[41]))
			if !poly1305.Verify(sum, magic, key) {
				err = errBadFrame
				return
			}
			plaintext = ciphertext[57:]

		}

		inbound = new(Inbound)
		var bs []byte
		bs, err = snappy.Decode(nil, plaintext)
		if err != nil {
			return
		}
		if err = sb.Copy(
			sb.Decode(bytes.NewReader(bs)),
			sb.Unmarshal(func(bs []byte, serial uint64) {
				inbound.WireData.Eth = bs
				inbound.WireData.Serial = serial
			}),
		); err != nil {
			return
		}

		return
	}
}
