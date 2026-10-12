import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  cacheComponents: true,
  partialPrefetching: true,
  // A self-contained server for the container image (deploy/docker/Dockerfile.web).
  output: "standalone",
};

export default nextConfig;
