# Set the base image to use for subsequent instructions
FROM node:slim

# Create a directory for the action code
RUN mkdir -p /usr/src/app

# Set the working directory inside the container.
WORKDIR /usr/src/app

# Copy the repository contents to the container
COPY . .

# Run the specified command within the container
ENTRYPOINT ["node", "/usr/src/app/dist/index.js"]
