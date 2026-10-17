import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "Zgo",
  identifier: "io.github.skeleton1231.zgo",
  version: "0.1.0",
  devUrl: "http://localhost:5173",
  devCommand: "bun run --cwd frontend dev:web",
  buildCommand: "bun run --cwd frontend build:web",
  frontendDist: "frontend/dist",
  bindings: "frontend/src/mygo.ts",
  out: "build",
});
