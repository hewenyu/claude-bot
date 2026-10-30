import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build goes into the Go module, where go:embed picks it up; it is
// committed so that `go install` users get the page without node.
export default defineConfig({
  plugins: [react()],
  base: "/",
  build: {
    outDir: "../internal/webui/static",
    emptyOutDir: true,
    sourcemap: false,
    chunkSizeWarningLimit: 600,
  },
  server: {
    // `npm run dev` talks to a running gateway.
    proxy: { "/api": { target: "http://127.0.0.1:7600", changeOrigin: true } },
  },
});
