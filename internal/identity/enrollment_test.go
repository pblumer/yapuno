package identity

import (
 "crypto/sha256"
 "errors"
 "testing"
 "time"
)

func TestApprovePairing(t *testing.T) {
 now:=time.Date(2026,11,4,12,0,0,0,time.UTC)
 digest:=sha256.Sum256([]byte("random-test-token"))
 valid:=Challenge{ChildID:"child-1",GuardianID:"parent-1",TokenDigest:digest,ExpiresAt:now.Add(time.Minute)}
 tests:=[]struct{name string; change func(*Challenge,*[32]byte,*string,*time.Time,*bool); want error}{
  {"valid",func(*Challenge,*[32]byte,*string,*time.Time,*bool){},nil},
  {"guardian not approved",func(_ *Challenge,_ *[32]byte,_ *string,_ *time.Time,b *bool){*b=false},ErrNotAuthorized},
  {"wrong guardian",func(_ *Challenge,_ *[32]byte,g *string,_ *time.Time,_ *bool){*g="stranger"},ErrNotAuthorized},
  {"missing child",func(c *Challenge,_ *[32]byte,_ *string,_ *time.Time,_ *bool){c.ChildID=""},ErrNotAuthorized},
  {"replayed",func(c *Challenge,_ *[32]byte,_ *string,_ *time.Time,_ *bool){c.Consumed=true},ErrConsumed},
  {"expired",func(_ *Challenge,_ *[32]byte,_ *string,n *time.Time,_ *bool){*n=n.Add(time.Minute)},ErrExpired},
  {"wrong token",func(_ *Challenge,d *[32]byte,_ *string,_ *time.Time,_ *bool){*d=sha256.Sum256([]byte("other"))},ErrInvalid},
  {"zero token",func(_ *Challenge,d *[32]byte,_ *string,_ *time.Time,_ *bool){*d=[32]byte{}},ErrInvalid},
 }
 for _,tt:=range tests {t.Run(tt.name,func(t *testing.T){
  c:=valid; d:=digest; g:="parent-1"; n:=now; allowed:=true
  tt.change(&c,&d,&g,&n,&allowed)
  if err:=ApprovePairing(c,d,g,n,allowed); !errors.Is(err,tt.want){t.Fatalf("got %v want %v",err,tt.want)}
 })}
}
