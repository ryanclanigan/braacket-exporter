import { expect, test } from "bun:test";
import { createConfig } from "../src/config";

test("createConfig derives the listing URL from the selected league slug", () => {
  const config = createConfig({ leagueSlug: "foo-league" });

  expect(config.leagueSlug).toBe("foo-league");
  expect(config.listingUrl).toBe("https://braacket.com/league/foo-league/tournament");
});
