benchmark:
	go test -bench=. -benchmem

benchmark-with-profile:
	go test -bench=. -benchmem -memprofile profilemem.out -cpuprofile profilecpu.out