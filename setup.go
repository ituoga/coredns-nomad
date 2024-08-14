package nomad

import (
	"strconv"
	"strings"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	nomad "github.com/hashicorp/nomad/api"
)

// init registers this plugin.
func init() { plugin.Register(pluginName, setup) }

// setup is the function that gets called when the config parser see the token "nomad". Setup is responsible
// for parsing any extra options the nomad plugin may have. The first token this function sees is "nomad".
func setup(c *caddy.Controller) error {
	n := &Nomad{
		ttl:     uint32(defaultTTL),
		clients: make([]*nomad.Client, 0),
		current: -1,
	}
	if err := parse(c, n); err != nil {
		return plugin.Error("nomad", err)
	}

	for idx, client := range n.clients {
		// Do a ping check to see if the Nomad server is reachable.
		_, err := client.Agent().Self()
		if err != nil {
			continue // Connection failed, try next client
		}

		n.current = idx // Set the current client
		break           // Connection succeeded, break the loop
	}

	// Mark the plugin as ready to use.
	// https://github.com/coredns/coredns/blob/master/plugin.md#readiness
	n.Ready()

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		n.Next = next
		return n
	})

	return nil
}

func parse(c *caddy.Controller, n *Nomad) error {
	cfg := nomad.DefaultConfig()

	addresses := []string{} // Multiple addresses are stored here

	for c.Next() {
		for c.NextBlock() {
			selector := strings.ToLower(c.Val())

			switch selector {
			case "address":
				// cfg.Address = c.RemainingArgs()[0]
				addresses = append(addresses, c.RemainingArgs()[0])
			case "token":
				cfg.SecretID = c.RemainingArgs()[0]
			case "zone":
				zone = c.RemainingArgs()[0]
			case "ttl":
				t, err := strconv.Atoi(c.RemainingArgs()[0])
				if err != nil {
					return c.Err("error parsing ttl: " + err.Error())
				}
				if t < 0 || t > 3600 {
					return c.Errf("ttl must be in range [0, 3600]: %d", t)
				}
				n.ttl = uint32(t)
			default:
				return c.Errf("unknown property '%s'", selector)
			}
		}
	}

	for _, addr := range addresses {
		cfg.Address = addr
		client, err := nomad.NewClient(cfg)
		if err != nil {
			return plugin.Error("nomad", err)
		}
		n.clients = append(n.clients, client) // Store all clients
	}

	return nil
}

func (n *Nomad) getClient() *nomad.Client {
	for i := 0; i < len(n.clients); i++ {
		idx := (n.current + i) % len(n.clients)
		_, err := n.clients[idx].Agent().Self()
		if err == nil {
			n.current = idx
			return n.clients[idx]
		} else {
			log.Error("getClient ", err)
		}
	}
	return nil
}
