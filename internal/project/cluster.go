package project

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/marcelsud/keel/core"
)

type localMember struct {
	Port    int                 `json:"port"`
	Cluster core.ClusterOptions `json:"cluster"`
}

// Local development keeps the three members in this process; deployed members
// use the same CLI independently with --cluster-config and separate volumes.
func startLocalCluster(root string, options core.Options) (string, func(), error) {
	path := filepath.Join(root, ".keel", "cluster.json")
	var members []localMember
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &members)
	}
	if errors.Is(err, os.ErrNotExist) {
		var reserved []net.Listener
		defer func() {
			for _, listener := range reserved {
				listener.Close()
			}
		}()
		address := func() (string, error) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return "", err
			}
			reserved = append(reserved, listener)
			return listener.Addr().String(), nil
		}
		id := sha256.Sum256([]byte(root))
		for i := range 3 {
			listen, e := address()
			if e != nil {
				return "", nil, e
			}
			sqlListen, e := address()
			if e != nil {
				return "", nil, e
			}
			client, e := address()
			if e != nil {
				return "", nil, e
			}
			_, port, _ := net.SplitHostPort(client)
			number, _ := strconv.Atoi(port)
			members = append(members, localMember{Port: number, Cluster: core.ClusterOptions{Name: fmt.Sprintf("dev-%x", id[:6]), Node: fmt.Sprintf("node-%d", i+1), Listen: listen, SQLListen: sqlListen, Replicas: 3}})
		}
		if options.Port >= 0 {
			members[0].Port = options.Port
			if members[0].Port == 0 {
				members[0].Port = 4222
			}
		}
		for i := range members {
			for j, peer := range members {
				if i != j {
					members[i].Cluster.Routes = append(members[i].Cluster.Routes, "nats-route://"+peer.Cluster.Listen)
				}
			}
			if i != 0 {
				members[i].Cluster.SQLPeers = []string{members[0].Cluster.SQLListen}
			}
		}
		data, err = json.MarshalIndent(members, "", "  ")
		if err == nil {
			err = os.WriteFile(path, data, 0600)
		}
		for _, listener := range reserved {
			listener.Close()
		}
		reserved = nil
	}
	if err != nil {
		return "", nil, err
	}
	if len(members) != 3 {
		return "", nil, errors.New("local cluster configuration must contain three members")
	}
	type result struct {
		node *core.Core
		err  error
	}
	ready := make(chan result, 3)
	for _, member := range members {
		go func() {
			config := options
			config.Port = member.Port
			config.Cluster = &member.Cluster
			config.DataDir = filepath.Join(options.DataDir, member.Cluster.Node)
			node, err := core.Start(config)
			ready <- result{node, err}
		}()
	}
	var nodes []*core.Core
	var urls []string
	var first error
	close := func() {
		for _, node := range nodes {
			node.Close()
		}
	}
	for range 3 {
		item := <-ready
		if item.err != nil {
			first = item.err
		} else {
			nodes = append(nodes, item.node)
			urls = append(urls, item.node.URL())
		}
	}
	if first != nil {
		close()
		return "", nil, first
	}
	return strings.Join(urls, ","), close, nil
}
