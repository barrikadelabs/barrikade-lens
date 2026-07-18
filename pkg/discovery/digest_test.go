package discovery

import "testing"

func TestDigestDoesNotMutateSnapshot(t *testing.T){snapshot:=validSnapshot();before:=snapshot.Evidence[0].ObservedAt;first,err:=snapshot.Digest();if err!=nil{t.Fatal(err)};second,err:=snapshot.Digest();if err!=nil{t.Fatal(err)};if first!=second{t.Fatal("digest is unstable")};if snapshot.Evidence[0].ObservedAt!=before{t.Fatal("digest mutated evidence observation time")}}
