import { defineConfig } from "vite";
import solid from "vite-plugin-solid";

export default defineConfig({
  plugins: [solid()],
  clearScreen: false,
  server: {
    port: 34116,
    strictPort: false,
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          markdown: ["markdown-it", "highlight.js"],
          solid: ["solid-js"],
        },
      },
    },
  },
});
