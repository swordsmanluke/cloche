package features_test

import "github.com/cucumber/godog"

func init() { registerScenarios(initSagaRemovalScenarios) }

func initSagaRemovalScenarios(ctx *godog.ScenarioContext) {
	// L1: image and docs
	ctx.Step(`^the DRA image build inputs \(Dockerfile and make docker-dra\)$`, func() error { return godog.ErrPending })                                  // pending: L1
	ctx.Step(`^neither installs a saga binary into the image$`, func() error { return godog.ErrPending })                                                  // pending: L1
	ctx.Step(`^the DRA image tests$`, func() error { return godog.ErrPending })                                                                            // pending: L1
	ctx.Step(`^none of them expect a saga binary to be present$`, func() error { return godog.ErrPending })                                                // pending: L1
	ctx.Step(`^the built DRA image$`, func() error { return godog.ErrPending })                                                                            // pending: L1
	ctx.Step(`^the image contents are inspected$`, func() error { return godog.ErrPending })                                                               // pending: L1
	ctx.Step(`^no saga binary is found on the image's PATH$`, func() error { return godog.ErrPending })                                                    // pending: L1
	ctx.Step(`^a DRA running in the saga-free image$`, func() error { return godog.ErrPending })                                                           // pending: L1
	ctx.Step(`^it runs "cloche questions ask" with a question$`, func() error { return godog.ErrPending })                                                 // pending: L1
	ctx.Step(`^the command succeeds and prints a question id$`, func() error { return godog.ErrPending })                                                  // pending: L1
	ctx.Step(`^a DRA running in the saga-free image that has asked a question$`, func() error { return godog.ErrPending })                                 // pending: L1
	ctx.Step(`^the question is answered and the DRA runs "cloche questions await" and "cloche questions read"$`, func() error { return godog.ErrPending }) // pending: L1
	ctx.Step(`^both commands print the answer$`, func() error { return godog.ErrPending })                                                                 // pending: L1
	ctx.Step(`^docs/dras.md and docs/SAFETY.md$`, func() error { return godog.ErrPending })                                                                // pending: L1
	ctx.Step(`^neither describes Saga access from the container$`, func() error { return godog.ErrPending })                                               // pending: L1
	ctx.Step(`^docs/dras.md describes asking via cloche questions ask, await and read$`, func() error { return godog.ErrPending })                         // pending: L1

	// L2: locked container and egress allowlist
	ctx.Step(`^a locked container is configured$`, func() error { return godog.ErrPending })                                     // pending: L2
	ctx.Step(`^its allowed environment and egress allowlist contain no Saga address$`, func() error { return godog.ErrPending }) // pending: L2
	ctx.Step(`^CLOCHE_SAGA_ADDR is set in the host environment$`, func() error { return godog.ErrPending })                      // pending: L2
	ctx.Step(`^the container receives no Saga address and no route to it$`, func() error { return godog.ErrPending })            // pending: L2
	ctx.Step(`^the repository source tree$`, func() error { return godog.ErrPending })                                           // pending: L2
	ctx.Step(`^searching it for "CLOCHE_SAGA_ADDR" finds nothing$`, func() error { return godog.ErrPending })                    // pending: L2
}
