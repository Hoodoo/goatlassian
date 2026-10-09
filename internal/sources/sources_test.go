package sources

import "testing"

func TestRemoteIdentity(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:Me/Repo.git":               "github.com/me/repo",
		"https://github.com/me/repo.git":           "github.com/me/repo",
		"https://user:tok@github.com/me/repo":      "github.com/me/repo",
		"ssh://git@gitlab.example.com/group/x.git": "gitlab.example.com/group/x",
		"": "",
	} {
		if got := RemoteIdentity(in); got != want {
			t.Errorf("RemoteIdentity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestURLs(t *testing.T) {
	if got := SessionsURL("http://b/", "/home/me/my repo"); got != "http://b/#/?project=/home/me/my%20repo" {
		t.Error(got)
	}
	if got := TagURL("http://b/", "sink:proto"); got != "http://b/#/?tag=sink:proto" {
		t.Error(got)
	}
	if got := WikiURL("http://o", "ws", "a b"); got != "http://o/#scope=ws:a%20b" {
		t.Error(got)
	}
}
