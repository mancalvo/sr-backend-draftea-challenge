// api-gateway is an intentional placeholder. In this system, Traefik serves
// as the API gateway via docker-compose, routing external traffic to the
// appropriate backend service. See deploy/traefik/dynamic.yml for routing rules.
package main

import "fmt"

func main() {
	fmt.Println("api-gateway starting...")
}
