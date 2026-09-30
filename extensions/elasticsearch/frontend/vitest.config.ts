import { defineConfig } from "vitest/config";

// The unit tests cover the page's pure logic (src/es/), its calls out (src/host.ts,
// against a stubbed window.__OPSKAT_EXT__) and the build's CSS scoping (tooling/);
// none imports the host's modules, so they run in plain node without the
// host-externals plugin.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts", "tooling/**/*.test.ts"],
  },
});
