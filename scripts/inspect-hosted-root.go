//go:build ignore
package main
import("crypto/ed25519";"crypto/sha256";"crypto/x509";"encoding/base64";"encoding/hex";"encoding/pem";"fmt";"os")
func main(){if len(os.Args)!=2{panic("public PEM path required")};raw,e:=os.ReadFile(os.Args[1]);if e!=nil{panic(e)};block,rest:=pem.Decode(raw);if block==nil||block.Type!="PUBLIC KEY"||len(rest)!=0{panic("invalid public PEM")};value,e:=x509.ParsePKIXPublicKey(block.Bytes);if e!=nil{panic(e)};key,ok:=value.(ed25519.PublicKey);if !ok{panic("Ed25519 root required")};digest:=sha256.Sum256(key);if hex.EncodeToString(digest[:])=="a96af130ee5a59294c90fcb8883dc7c6c3878365d970f4a6e67fa7ed2fc7ef4c"{panic("development root forbidden in release")};fmt.Print(base64.RawURLEncoding.EncodeToString(key))}
