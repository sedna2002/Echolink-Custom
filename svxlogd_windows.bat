@echo off

set GOOS=windows
set GOARCH=amd64

set CGO_ENABLED=0


go build -o svxlogd.exe svxlogd.go