import type { NextConfig } from "next";
import path from "node:path";

const nextConfig: NextConfig = {
  // The repo root carries an unrelated package-lock.json from outside this app, which otherwise
  // makes Next.js guess the workspace root incorrectly.
  turbopack: {
    root: path.join(__dirname),
  },
};

export default nextConfig;
