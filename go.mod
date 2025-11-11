module svxlogd

go 1.24.4

replace (
	ExtensionLinux => "C:/Users/Frederic/Documents/PERSONNEL/DEV PERSO/GOModules/ExtensionLinux"
	LogX => "C:/Users/Frederic/Documents/PERSONNEL/DEV PERSO/GOModules/LogX"
)

require (
	ExtensionLinux v0.0.0-00010101000000-000000000000
	LogX v0.0.0-00010101000000-000000000000
	github.com/go-sql-driver/mysql v1.9.3
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/text v0.21.0 // indirect
)
