import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    include: ["test/**/*.test.ts"],
    testTimeout: 60_000,
    hookTimeout: 60_000,
    // Integration tests spawn nats-server; run them sequentially so the
    // bound ports don't collide across workers.
    fileParallelism: false,
  },
});
