// Copyright 2018-2019 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/nats-io/nkeys"
)

// this will be set during compilation when a release is made on tools
var Version string

func usage() {
	log.Fatalf("Usage: nk [-v] [-gen type] [-sign content] [-verify content] [-inkey key] [-pubin publickey] [-sig signature] [-pubout] [-e entropy] [-pre vanity]\n")
}

func main() {
	var entropy = flag.String("e", "", "Entropy, e.g. /dev/urandom")
	var key = flag.String("inkey", "", "Input key  (seed/private key)")
	var pub = flag.String("pubin", "", "Public key ")

	var signContent = flag.String("sign", "", "Sign <file> with -inkey <key>")
	var sig = flag.String("sig", "", "Signature content")

	var verifyContent = flag.String("verify", "", "Verify content with -inkey <key> or -pubin <public> and -sig <file>")

	var keyType = flag.String("gen", "", "Generate key for <type>, e.g. nk -gen user")
	var pubout = flag.Bool("pubout", false, "Output public key")

	var version = flag.Bool("v", false, "Show version")
	var vanPre = flag.String("pre", "", "Attempt to generate public key given prefix, e.g. nk -gen user -pre derek")
	var vanMax = flag.Int("maxpre", 10000000, "Maximum attempts at generating the correct key prefix")

	log.SetFlags(0)
	log.SetOutput(os.Stdout)

	flag.Usage = usage
	flag.Parse()

	if *version {
		fmt.Printf("nk version %s\n", Version)
	}

	// Create Key
	if *keyType != "" {
		var kp nkeys.KeyPair
		// Check to see if we are trying to do a vanity public key.
		if *vanPre != "" {
			kp = createVanityKey(*keyType, *vanPre, *entropy, *vanMax)
		} else {
			kp = genKeyPair(preForType(*keyType), *entropy)
		}
		seed, err := kp.Seed()
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("%s", seed)
		if *pubout || *vanPre != "" {
			pub, _ := kp.PublicKey()
			log.Printf("%s", pub)
		}
		return
	}

	if *entropy != "" {
		log.Fatalf("Entropy file only used when creating keys with -gen")
	}

	// Sign
	if *signContent != "" {
		sign(*signContent, *key)
		return
	}

	// Verfify
	if *verifyContent != "" {
		verify(*verifyContent, *key, *pub, *sig)
		return
	}

	// Show public key from seed/private
	if *key != "" && *pubout {
		printPublicFromSeed(*key)
		return
	}

	usage()
}

func printPublicFromSeed(keyFile string) {
	seed := readKey(keyFile)

	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		log.Fatal(err)
	}
	pub, _ := kp.PublicKey()
	log.Printf("%s", pub)
}

func sign(name, key string) {
	if key == "" {
		log.Fatalf("Sign requires a seed/private key via -inkey <file>")
	}
	seed := readKey(key)
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		log.Fatal(err)
	}

	content := []byte(name)

	sigraw, err := kp.Sign(content)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%s", base64.StdEncoding.EncodeToString(sigraw))
}

func verify(fname, keyFile, pubFile, sigFile string) {
	if keyFile == "" && pubFile == "" {
		log.Fatalf("Verify requires a seed key via -inkey or a public key via -pubin")
	}
	if sigFile == "" {
		log.Fatalf("Verify requires a signature via -sigfile")
	}
	var err error
	var kp nkeys.KeyPair
	if keyFile != "" {
		var seed []byte = []byte(keyFile)

		if err != nil {
			log.Fatal(err)
		}
		kp, err = nkeys.FromSeed(seed)
	} else {
		// Public Key
		var public []byte = []byte(pubFile)

		if err != nil {
			log.Fatal(err)
		}
		kp, err = nkeys.FromPublicKey(string(public))
	}
	if err != nil {
		log.Fatal(err)
	}

	content := []byte(fname)

	sig, err := base64.StdEncoding.DecodeString(sigFile)
	if err != nil {
		log.Fatal(err)
	}
	if err := kp.Verify(content, sig); err != nil {
		log.Fatal(err)
	}
	log.Printf("Verified OK")
}

func preForType(keyType string) nkeys.PrefixByte {
	keyType = strings.ToLower(keyType)
	switch keyType {
	case "user":
		return nkeys.PrefixByteUser
	case "account":
		return nkeys.PrefixByteAccount
	case "server":
		return nkeys.PrefixByteServer
	case "cluster":
		return nkeys.PrefixByteCluster
	case "operator":
		return nkeys.PrefixByteOperator
	default:
		log.Fatalf("Usage: nk -gen [user|account|server|cluster|operator]\n")
	}
	return nkeys.PrefixByte(0)
}

func genKeyPair(pre nkeys.PrefixByte, entropy string) nkeys.KeyPair {
	// See if we override entropy.
	ef := rand.Reader
	if entropy != "" {
		r, err := os.Open(entropy)
		if err != nil {
			log.Fatal(err)
		}
		ef = r
	}

	// Create raw seed from source or random.
	var rawSeed [32]byte
	_, err := io.ReadFull(ef, rawSeed[:]) // Or some other random source.
	if err != nil {
		log.Fatalf("Error reading from %s: %v", ef, err)
	}
	kp, err := nkeys.FromRawSeed(pre, rawSeed[:])
	if err != nil {
		log.Fatalf("Error creating %c: %v", pre, err)
	}
	return kp
}

var b32Enc = base32.StdEncoding.WithPadding(base32.NoPadding)

func createVanityKey(keyType, vanity, entropy string, max int) nkeys.KeyPair {
	spinners := []rune(`??????????????????????????????`)
	pre := preForType(keyType)
	vanity = strings.ToUpper(vanity)
	// Check to make sure we can base32 into it by trying to decode it.
	_, err := b32Enc.DecodeString(vanity)
	if err != nil {
		log.Fatalf("Can not generate base32 encoded strings to match '%s'", vanity)
	}

	ncpu := runtime.NumCPU()

	// Work channel
	wch := make(chan struct{})
	defer close(wch)

	// Found solution
	found := make(chan nkeys.KeyPair)

	// Start NumCPU go routines.
	for i := 0; i < ncpu; i++ {
		go func() {
			for range wch {
				kp := genKeyPair(pre, entropy)
				pub, _ := kp.PublicKey()
				if strings.HasPrefix(pub[1:], vanity) {
					found <- kp
					return
				}
			}
		}()
	}

	// Run through max iterations.
	for i := 0; i < max; i++ {
		spin := spinners[i%len(spinners)]
		fmt.Fprintf(os.Stderr, "\r\033[mcomputing\033[m %s ", string(spin))
		wch <- struct{}{}
		select {
		case kp := <-found:
			fmt.Fprintf(os.Stderr, "\r")
			return kp
		default:
		}
	}

	fmt.Fprintf(os.Stderr, "\r")
	log.Fatalf("Failed to generate prefix after %d attempts", max)
	return nil
}

func readKey(filename string) []byte {
	var key []byte
	var contents = []byte(filename)
	defer wipeSlice(contents)

	lines := bytes.Split(contents, []byte("\n"))
	for _, line := range lines {
		if nkeys.IsValidEncoding(line) {
			key = make([]byte, len(line))
			copy(key, line)
			return key
		}
	}
	if key == nil {
		log.Fatalf("Could not find a valid key")
	}
	return key
}

func wipeSlice(buf []byte) {
	for i := range buf {
		buf[i] = 'x'
	}
}
