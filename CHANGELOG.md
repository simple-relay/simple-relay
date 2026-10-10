# Changelog

## [0.4.0](https://github.com/simple-relay/simple-relay/compare/v0.3.0...v0.4.0) (2026-10-10)


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
* **ui:** automate dashboard version sync via release-please extra-files ([#36](https://github.com/simple-relay/simple-relay/issues/36)) ([6fbc630](https://github.com/simple-relay/simple-relay/commit/6fbc6309601b3c8b20d19e890a1a7c60b65fe82a))
* **ui:** implement embedded zero-dependency dashboard and admin APIs ([#6](https://github.com/simple-relay/simple-relay/issues/6)) ([0d7023b](https://github.com/simple-relay/simple-relay/commit/0d7023b97c47922f406f2601e675170ba9e867a9))
* **ui:** refactor route creation to slide-over drawer and sort newest routes first ([#29](https://github.com/simple-relay/simple-relay/issues/29)) ([8d9044a](https://github.com/simple-relay/simple-relay/commit/8d9044a360780d17bc39e18c8b4c58466e24828d))
* **ui:** update tab labels, add request log action, and move delete to drawer danger zone ([#40](https://github.com/simple-relay/simple-relay/issues/40)) ([d8a3479](https://github.com/simple-relay/simple-relay/commit/d8a347963e399767d39fc5e7d749893b965a5109))


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
* **main:** release 0.2.0 ([ee5e7fe](https://github.com/simple-relay/simple-relay/commit/ee5e7fe6e2706a8efaf36a5005e9829ea7eb3dd8))
* **main:** release 0.3.0 ([#37](https://github.com/simple-relay/simple-relay/issues/37)) ([d4c098f](https://github.com/simple-relay/simple-relay/commit/d4c098ff64397d38c75ee29c13a735dfed105e62))
* Update ignore ([4b3fc5e](https://github.com/simple-relay/simple-relay/commit/4b3fc5e8a88502578b0056e3a7123e2279faf085))

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
