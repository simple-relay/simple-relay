# Changelog

## [0.8.0](https://github.com/simple-relay/simple-relay/compare/v0.7.0...v0.8.0) (2026-10-17)


### Features

* **auth:** implement api key authentication for programmatic management ([#68](https://github.com/simple-relay/simple-relay/issues/68)) ([5b6125b](https://github.com/simple-relay/simple-relay/commit/5b6125b1f431f05c68b8e653f208bec276508e54))
* **cli:** merge operator tooling into single relay binary ([#75](https://github.com/simple-relay/simple-relay/issues/75)) ([bfbb5a6](https://github.com/simple-relay/simple-relay/commit/bfbb5a67d444682d3d1a82b1c6e6845400c5a997))
* **scripts:** implement e2e webhook and relay simulation tool ([#70](https://github.com/simple-relay/simple-relay/issues/70)) ([8a7716d](https://github.com/simple-relay/simple-relay/commit/8a7716dbaaf7f9aeb64c73a81815959e46a15caf))
* **simulator:** update web ui following design system specifications ([#77](https://github.com/simple-relay/simple-relay/issues/77)) ([6fdbdac](https://github.com/simple-relay/simple-relay/commit/6fdbdac8e725c5207dd9d05fc1e5ad8e93551cff))
* **ui:** apply minor UI updates and embed dashboard icons ([#64](https://github.com/simple-relay/simple-relay/issues/64)) ([fd78abc](https://github.com/simple-relay/simple-relay/commit/fd78abc399007c0172a9ca00a9da5330892c0124))
* **ui:** implement api key management in dashboard ui ([#72](https://github.com/simple-relay/simple-relay/issues/72)) ([587dc71](https://github.com/simple-relay/simple-relay/commit/587dc71e886ac0100698f679d41f24c5386db03d))
* **ui:** redesign dashboard layout and streamline navigation ([#74](https://github.com/simple-relay/simple-relay/issues/74)) ([479c339](https://github.com/simple-relay/simple-relay/commit/479c339f98e11971a493c68f717c77dddbf39ae8))

## [0.7.0](https://github.com/simple-relay/simple-relay/compare/v0.6.0...v0.7.0) (2026-10-16)


### Features

* **auth:** implement admin password session auth and login rate limiter ([1158fee](https://github.com/simple-relay/simple-relay/commit/1158fee65167a7ad5aa9d2d11720784186b15f4e))


### Bug Fixes

* **ci:** replace builds with ids in goreleaser docker config ([#67](https://github.com/simple-relay/simple-relay/issues/67)) ([739621e](https://github.com/simple-relay/simple-relay/commit/739621e65b0a4516f36a1d848a65bee67765e6dd))


### Maintenance & Dependencies

* synchronize roadmap with issue status [skip ci] ([3d57521](https://github.com/simple-relay/simple-relay/commit/3d575216e99d88b30e519be5ca73665f422c4838))

## [0.6.0](https://github.com/simple-relay/simple-relay/compare/v0.5.0...v0.6.0) (2026-10-15)


### Features

* **cli:** implement relay inspection binary cmd/relay-cli ([#62](https://github.com/simple-relay/simple-relay/issues/62)) ([8bacf5e](https://github.com/simple-relay/simple-relay/commit/8bacf5e088866f9d14d80f463d07b0a6de3383d6))
* **signature:** add asymmetric verification and custom validation pipeline ([#61](https://github.com/simple-relay/simple-relay/issues/61)) ([34e741f](https://github.com/simple-relay/simple-relay/commit/34e741f698c76329a09023296f5f21332a81780c))
* **ui:** add custom validation option for routes ([#65](https://github.com/simple-relay/simple-relay/issues/65)) ([442dfa8](https://github.com/simple-relay/simple-relay/commit/442dfa8d710ac3acbfc6fd87fc283e9576f48262))


### Documentation

* **readme:** add logo beside project name in header ([#57](https://github.com/simple-relay/simple-relay/issues/57)) ([63d6bb5](https://github.com/simple-relay/simple-relay/commit/63d6bb5df5ee9ce20e105a33dd3d82ae0921f4f1))
* **specs:** add api key authentication design spec ([#63](https://github.com/simple-relay/simple-relay/issues/63)) ([c6daf31](https://github.com/simple-relay/simple-relay/commit/c6daf310e9a45d251a5c12d9dbaa016d092a0941))

## [0.5.0](https://github.com/simple-relay/simple-relay/compare/v0.4.0...v0.5.0) (2026-10-13)


### Features

* **engine:** implement configurable dispatcher concurrency and active wal checkpointing ([#56](https://github.com/simple-relay/simple-relay/issues/56)) ([e4a23e4](https://github.com/simple-relay/simple-relay/commit/e4a23e43a2364ea8befb94755671c46847e09ad6))
* **upgrade:** implement zero-downtime live binary upgrade and socket handoff ([#54](https://github.com/simple-relay/simple-relay/issues/54)) ([e0a1ba0](https://github.com/simple-relay/simple-relay/commit/e0a1ba04568e10d28ec217971ab4bc1e7a057784))


### Documentation

* **specs:** add configurable dispatcher concurrency and wal checkpointing design spec  ([#55](https://github.com/simple-relay/simple-relay/issues/55)) ([8b320eb](https://github.com/simple-relay/simple-relay/commit/8b320eb629d9d6ab06e5391dc70e63cb323e5485))
* **specs:** add zero-downtime live upgrade technical design specification ([#51](https://github.com/simple-relay/simple-relay/issues/51)) ([#52](https://github.com/simple-relay/simple-relay/issues/52)) ([4c80098](https://github.com/simple-relay/simple-relay/commit/4c800984d12b504a6d442ff8fd9e11c5be2c4293))


### Maintenance & Dependencies

* synchronize roadmap with issue status [skip ci] ([03092fb](https://github.com/simple-relay/simple-relay/commit/03092fbabcf4c1595ef23208356b17d6dd5e2d2e))
* synchronize roadmap with issue status [skip ci] ([7405785](https://github.com/simple-relay/simple-relay/commit/7405785da8009e56b2197c9b8072cbfe640e61f8))

## [0.4.0](https://github.com/simple-relay/simple-relay/compare/v0.3.0...v0.4.0) (2026-10-10)


### Features

* **bench:** add automated E2E benchmark suite, v0.3.0 report, and roadmap item ([#45](https://github.com/simple-relay/simple-relay/issues/45)) ([#46](https://github.com/simple-relay/simple-relay/issues/46)) ([3cd8f6d](https://github.com/simple-relay/simple-relay/commit/3cd8f6df13edb021e95d2776bc07e2e5985e1c0a))
* **dev:** add debug and debug-dlv targets to Makefile ([#43](https://github.com/simple-relay/simple-relay/issues/43)) ([00e92c2](https://github.com/simple-relay/simple-relay/commit/00e92c2afbcfabc2d68509e91b07648aff22ecff))


### Documentation

* **roadmap:** track dynamic url dashboard task ([#49](https://github.com/simple-relay/simple-relay/issues/49)) ([68e51cf](https://github.com/simple-relay/simple-relay/commit/68e51cfef6200b7da47b9c6a5146db35c0a9e528))


### Maintenance & Dependencies

* Update roadmap.md ([#48](https://github.com/simple-relay/simple-relay/issues/48)) ([e0abf23](https://github.com/simple-relay/simple-relay/commit/e0abf23ee59d9374bffd1b244b6e05268d044e6d))

## [0.3.0](https://github.com/simple-relay/simple-relay/compare/v0.2.0...v0.3.0) (2026-10-10)


### Features

* **ui:** automate dashboard version sync via release-please extra-files ([#36](https://github.com/simple-relay/simple-relay/issues/36)) ([6fbc630](https://github.com/simple-relay/simple-relay/commit/6fbc6309601b3c8b20d19e890a1a7c60b65fe82a))
* **ui:** update tab labels, add request log action, and move delete to drawer danger zone ([#40](https://github.com/simple-relay/simple-relay/issues/40)) ([d8a3479](https://github.com/simple-relay/simple-relay/commit/d8a347963e399767d39fc5e7d749893b965a5109))

## [0.2.0](https://github.com/simple-relay/simple-relay/compare/v0.1.0...v0.2.0) (2026-10-10)


### Features

* **api:** implement durable inbound webhook ingestion pipeline ([#2](https://github.com/simple-relay/simple-relay/issues/2)) ([1d2624b](https://github.com/simple-relay/simple-relay/commit/1d2624b8d550377ad63cabadb7f32a1d7cca3128))
* **ci:** automate versioning with release-please and cross-platform builds with goreleaser ([#19](https://github.com/simple-relay/simple-relay/issues/19), [#18](https://github.com/simple-relay/simple-relay/issues/18)) ([#33](https://github.com/simple-relay/simple-relay/issues/33)) ([1332888](https://github.com/simple-relay/simple-relay/commit/1332888c1379dbc0c0e44d30cb80b98ed91892cc))
* dlq replay and version spec ([#3](https://github.com/simple-relay/simple-relay/issues/3)) ([284b183](https://github.com/simple-relay/simple-relay/commit/284b18384e3eacd7eb427ec809cee7a2842181d8))
* **docs:** setup github issue templates, contributing guide, and roadmap sync automation ([#7](https://github.com/simple-relay/simple-relay/issues/7)) ([4367cde](https://github.com/simple-relay/simple-relay/commit/4367cde35d93d86a32bfc1419fd60030ea6e730e))
* **engine:** implement storage retention cleaner and vacuum worker ([#15](https://github.com/simple-relay/simple-relay/issues/15)) ([#32](https://github.com/simple-relay/simple-relay/issues/32)) ([447500b](https://github.com/simple-relay/simple-relay/commit/447500bfd1c2d70de3d6a32e57416a400c2a2cb8))
* Implement asynchronous outbound durable jobs and delivery pipeline ([#1](https://github.com/simple-relay/simple-relay/issues/1)) ([825dd73](https://github.com/simple-relay/simple-relay/commit/825dd73a25741613d1189d1c52b17023a63aef40))
* Implement asynchronous outbound durable jobs and delivery pipeline (Milestone 1.3) ([825dd73](https://github.com/simple-relay/simple-relay/commit/825dd73a25741613d1189d1c52b17023a63aef40))
* Implement outbound retry engine and synchronous HTTP proxy ([c12db1f](https://github.com/simple-relay/simple-relay/commit/c12db1f2359a9117057023ce7607f38519953b19))
* **signature:** implement cryptographic webhook signature verification adapters ([#5](https://github.com/simple-relay/simple-relay/issues/5)) ([0aef0d7](https://github.com/simple-relay/simple-relay/commit/0aef0d7470b960f83d33096c1f6d71030829b7c7))
* **ui:** implement embedded zero-dependency dashboard and admin APIs ([#6](https://github.com/simple-relay/simple-relay/issues/6)) ([0d7023b](https://github.com/simple-relay/simple-relay/commit/0d7023b97c47922f406f2601e675170ba9e867a9))
* **ui:** refactor route creation to slide-over drawer and sort newest routes first ([#29](https://github.com/simple-relay/simple-relay/issues/29)) ([8d9044a](https://github.com/simple-relay/simple-relay/commit/8d9044a360780d17bc39e18c8b4c58466e24828d))


### Code Refactoring

* **roadmap:** streamline phases and remove milestones and task prefixes ([#31](https://github.com/simple-relay/simple-relay/issues/31)) ([b7b9411](https://github.com/simple-relay/simple-relay/commit/b7b9411b03ce50f1f48addbac312b7673af7404d))


### Documentation

* add README.md and unify project name to Simple Relay ([1ddb4e8](https://github.com/simple-relay/simple-relay/commit/1ddb4e83db3e30614c0ae070fcb81cf5c65b30f9))
* Add roadmap.md ([2ca3a24](https://github.com/simple-relay/simple-relay/commit/2ca3a248e76bbb3fdc4b3b87fe0ed29694ec7440))
* Move documentation to docs/ and update references ([119f882](https://github.com/simple-relay/simple-relay/commit/119f882b2d586b10fe4504051e2a0800c19e3912))
* **specs:** add database migrations engine design and update roadmap ([#4](https://github.com/simple-relay/simple-relay/issues/4)) ([db74ce2](https://github.com/simple-relay/simple-relay/commit/db74ce2b40ba3f562fb7a99fd7f6edad439631f1))


### Maintenance & Dependencies

* Add agents.md ([d64f762](https://github.com/simple-relay/simple-relay/commit/d64f7626952c12717a32877abbc99c6ac4d57f34))
* Init the first files ([ced9ac5](https://github.com/simple-relay/simple-relay/commit/ced9ac5c03ecbdcb8dda2de2e0038a25d316512d))
* Update ignore ([4b3fc5e](https://github.com/simple-relay/simple-relay/commit/4b3fc5e8a88502578b0056e3a7123e2279faf085))
