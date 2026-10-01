import { createApi } from "./api";

const command = process.argv[2];
if (command !== "setup" && command !== "recover" && command !== "backup") {
  console.error(
    "Usage: scratchpad-admin setup|recover|backup <absolute-destination>",
  );
  process.exitCode = 1;
} else {
  const api = createApi({
    databaseUrl: process.env.SCRATCHPAD_DATABASE_URL,
    databasePath:
      process.env.SCRATCHPAD_DATABASE_PATH ?? "data/scratchpad.sqlite",
    origin: process.env.SCRATCHPAD_PUBLIC_URL ?? "http://localhost:3000",
  });
  try {
    if (command === "backup") {
      const destination = process.argv[3];
      if (!destination)
        throw new Error("Backup requires an absolute destination path.");
      await api.store.backup(destination);
      console.log(`Backup created: ${destination}`);
    } else {
      const token = await api.auth.createSetupToken(command === "recover");
      console.log(
        `Open ${process.env.SCRATCHPAD_PUBLIC_URL ?? "http://localhost:3000"} and enter this single-use ${command} token (expires in 15 minutes):`,
      );
      console.log(token);
    }
  } catch (error) {
    console.error(
      error instanceof Error ? error.message : "Administrative command failed.",
    );
    process.exitCode = 1;
  } finally {
    api.close();
  }
}
