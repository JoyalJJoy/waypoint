/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // Keep the repository clean: the framework can generate its own agent notes.
  agentRules: false,
};

export default nextConfig;
