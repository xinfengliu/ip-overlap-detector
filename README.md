## Detect Docker Overlay Network IP Overlapping

The tool runs as docker swarm services on docker bridge network.

### Usage:
`docker stack deploy -c docker-stack.yaml iod`

Then watch the logs of service `iod_manager`:
`docker service logs iod_manager -f`
