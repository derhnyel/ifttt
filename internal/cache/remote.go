package cache

import "time"

type RemoteEntry struct {
	Data      []byte
	ETag      string
	FetchedAt time.Time
}

func LoadRemote(key string, entry *RemoteEntry) error  { return Load("remote:"+key, entry) }
func StoreRemote(key string, entry *RemoteEntry) error { return Store("remote:"+key, entry) }
