## Detect Docker Overlay Network IP Overlapping

The tool runs as docker swarm services on docker bridge network.

### Usage:
`docker stack deploy -c docker-stack.yaml iod`

Then watch the logs of service `iod_manager`:
`docker service logs iod_manager -f`

### _Warning_
>This docker stack will not work if `selinux` is enabled in docker daemon, it is because of permission issue in accessing docker/swarm unix domain socket.
Unfortunately, docker stack in swarm mode does not support `security_opts` or `privileged` options.
