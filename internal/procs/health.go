package procs

import (
	"net/http"
	"strconv"
	"time"
)

// portOwnedBy reports whether `port` is served by a live PocketBase process with
// the given pid. Because the allocator never hands the same port to two
// instances, an answering health endpoint plus a live pid is sufficient evidence
// that the recorded process still owns the port.
func portOwnedBy(port, pid int) bool {
	if port <= 0 || !PidAlive(pid) {
		return false
	}
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
