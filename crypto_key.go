package l2

import "hash/fnv"

type CryptoKey []byte

func (Network) CryptoKey() CryptoKey {
	panic("not provide")
}

type CryptoKeyInt uint64

func (Network) CryptoKeyInt(
	key CryptoKey,
) CryptoKeyInt {
	h := fnv.New64a()
	h.Write(key)
	return CryptoKeyInt(h.Sum64())
}
