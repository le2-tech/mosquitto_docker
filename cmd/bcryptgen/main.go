package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	cost := flag.Int("cost", 12, "bcrypt cost (10-14 recommended)")
	flag.Parse()

	var pass string
	if flag.NArg() > 0 {
		pass = flag.Arg(0)
	} else {
		fmt.Print("Password: ")
		reader := bufio.NewReader(os.Stdin)
		p, _ := reader.ReadString('\n')
		pass = p
	}
	pass = trimNL(pass)
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), *cost)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(hash))
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
