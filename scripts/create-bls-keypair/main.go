package main

import (
	"fmt"
	"log"

	"github.com/flashbots/go-boost-utils/bls"
	"github.com/flashbots/go-boost-utils/types"
)

func main() {
	sk, _, err := bls.GenerateNewKeypair()
	if err != nil {
		log.Fatal(err.Error())
	}

	pubkey, err := types.BlsPublicKeyToPublicKey(bls.PublicKeyFromSecretKey(sk))
	if err != nil {
		log.Fatal(err.Error())
	}

	fmt.Printf("secret key: 0x%x\n", sk.Serialize())
	fmt.Printf("public key: %s\n", pubkey.String())
}
