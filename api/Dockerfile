FROM node:22-alpine

WORKDIR /app

# Install production deps only
COPY package*.json ./
RUN npm ci --omit=dev

# Copy application source
COPY src/ src/

ENV PORT=8080
EXPOSE 8080

CMD ["node", "src/index.js"]
