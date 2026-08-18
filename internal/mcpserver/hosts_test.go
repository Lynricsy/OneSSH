package mcpserver

import (
	"context"
	"reflect"
	"testing"

	"onessh/internal/cryptox"
	"onessh/internal/sshpool"
	"onessh/internal/store"
)

func TestHostsListReturnsTagsForAuthorizedHosts(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.CreateHost(ctx, store.Host{
		Name: "web-01", Addr: "10.0.0.1", Port: 22, Username: "root", AuthType: "password",
		Tags: []string{"prod", "web"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateHost(ctx, store.Host{
		Name: "db-01", Addr: "10.0.0.2", Port: 22, Username: "root", AuthType: "password",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.CreateToken(ctx, store.TokenCreate{Name: "agent", Hash: store.TokenHash("secret"), AllHosts: true}); err != nil {
		t.Fatal(err)
	}

	_, hosts, err := st.FindTokenForResource(ctx, store.TokenHash("secret"), "")
	if err != nil {
		t.Fatal(err)
	}
	allowed := make(map[string]store.Host, len(hosts))
	for _, host := range hosts {
		allowed[host.Name] = host
	}

	box, err := cryptox.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	pool := sshpool.New(st, box)
	defer pool.Close()
	server := &Server{Pool: pool}
	_, out, err := server.hostsList(context.WithValue(ctx, principalKey{}, Principal{Hosts: allowed, Store: st}), nil, Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Hosts) != 2 {
		t.Fatalf("主机数量 = %d", len(out.Hosts))
	}

	got := map[string]HostItem{}
	for _, item := range out.Hosts {
		got[item.Name] = item
	}
	if !reflect.DeepEqual(got["web-01"].Tags, []string{"prod", "web"}) {
		t.Fatalf("带标签主机 tags = %#v", got["web-01"].Tags)
	}
	if got["db-01"].Tags == nil || len(got["db-01"].Tags) != 0 {
		t.Fatalf("无标签主机 tags = %#v，期望空数组", got["db-01"].Tags)
	}
}
