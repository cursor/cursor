# ==============================================================================
# Dockerfile for 24/7 VPS Deployment
# Antigravity Gemini 3.7 & Exness MT5 Node Service
# Managed by google.com.jules[bot]
# ==============================================================================

FROM node:18-alpine

WORKDIR /app

# Copy configuration and code files
COPY package*.json ./
RUN npm install --production || true

COPY . .

# Expose Antigravity Node Server port
EXPOSE 3000

# Set environment variables
ENV NODE_ENV=production
ENV NODE_SERVER_PORT=3000
ENV GEMINI_MODEL=gemini-3.7-flash

# Health check endpoint
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3   CMD wget --quiet --tries=1 --spider http://localhost:3000/health

# Start server daemon
CMD ["node", "scripts/node-gemini-server.js"]
