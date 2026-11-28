import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    // The Go API listens on :8000 (VAULT_UI_LISTEN); the admin server holds
    // :9090. During development the page runs on the Vite server and proxies
    // /api and /events to the Go process, so it reads the same endpoints the
    // embedded build does.
    proxy: {
      "/api": "http://127.0.0.1:8000",
      "/events": "http://127.0.0.1:8000",
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/setupTests.ts"],
    // e2e/ would hold Playwright specs, whose test() API cannot run under
    // Vitest. vault-ui keeps its browser suite in Go (../e2e), but the exclude
    // stays so a future Node spec cannot be picked up by accident.
    exclude: ["node_modules/**", "dist/**", "e2e/**"],
  },
});
