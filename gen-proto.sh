# old version (using google.golang.org/grpc v1.27.1 )
# go get -u github.com/golang/protobuf/{proto,protoc-gen-go} 
# I use the old version for this project
protoc -I  ~/pkg/protoc-3.15.8/include:. --go_out=plugins=grpc:. --go_opt=paths=source_relative api/*.proto

# new version 
# https://grpc.io/docs/languages/go/quickstart/
# go get google.golang.org/protobuf/cmd/protoc-gen-go \
#         google.golang.org/grpc/cmd/protoc-gen-go-grpc

#protoc --go_out=. --go_opt=paths=source_relative \
#  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
#    api/helloworld.proto
