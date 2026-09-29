import { defineConfig } from "vitest/config";

// The unit tests cover the page's pure logic (src/es/) and the build's CSS
// scoping (tooling/); neither imports the host, so they run in plain node without
// the host-externals plugin.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts", "tooling/**/*.test.ts"],
  },
});
