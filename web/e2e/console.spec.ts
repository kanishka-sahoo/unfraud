import { expect, test } from "@playwright/test";

test.beforeEach(({ page }) => {
  page.on("pageerror", (e) => {
    throw e;
  });
});

test("the dashboard shows the figures and highest-risk alerts", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1, name: "Dashboard" })).toBeVisible();
  await expect(page.getByText("Payments scored")).toBeVisible();
  await expect(page.getByRole("img", { name: /Decisions per minute/ }).or(page.getByText("No decisions in the last hour"))).toBeVisible();
  await expect(page.getByRole("heading", { name: "Highest-risk open alerts" })).toBeVisible();
});

test("an alert explains its score, and a disposition is recorded", async ({ page }) => {
  await page.goto("/alerts?sort=score");
  const first = page.locator("table.t tbody tr").first().getByRole("link");
  await first.click();
  await expect(page).toHaveURL(/\/alerts\/[^/?]+$/);
  const id = decodeURIComponent(page.url().split("/").pop()!);

  await expect(page.getByRole("heading", { name: "Why it was flagged" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Payment details" })).toBeVisible();
  await page.getByText("Show the full score breakdown").click();
  await expect(page.getByText("Base rate (the model's average payment)")).toBeVisible();
  await expect(page.locator(".ledger tr.balance td").last()).toHaveText(/%$/);
  await expect(page.getByRole("heading", { name: "Summary", exact: true })).toBeVisible();

  await page.getByLabel("Note for the record").fill("e2e: checked the payment network");
  await page.getByRole("button", { name: "Escalate" }).click();
  await expect(page.getByText("e2e: checked the payment network").first()).toBeVisible();
  await expect(page.getByRole("status")).toContainText("Escalated");

  await page.goto("/alerts?status=disposed&sort=recent");
  await expect(page.locator(`a[href="/alerts/${encodeURIComponent(id)}"]`)).toBeVisible();
});

test("merchant category codes are spelled out", async ({ page }) => {
  await page.goto("/alerts?sort=score");
  await expect(page.locator("table.t tbody tr").first()).toContainText(/MCC \d{4}/);
});

test("the network view draws a card's neighbourhood", async ({ page }) => {
  await page.goto("/network");
  const card = page.locator("table.t tbody tr").first().getByRole("link");
  await card.click();
  await expect(page.getByRole("heading", { level: 1, name: /Card/ })).toBeVisible();
  // A drawn map, or an explained empty state.
  await expect(
    page.getByRole("img", { name: /merchants, \d+ other cards/ })
      .or(page.getByText(/No links in the current window|The network is unavailable/)),
  ).toBeVisible();
});

test("the live feed connects", async ({ page }) => {
  await page.goto("/live");
  await expect(page.getByRole("status")).toContainText("Connected", { timeout: 15_000 });
});

test("benchmarks show the generated results", async ({ page }) => {
  await page.goto("/benchmarks");
  await expect(page.getByRole("heading", { name: /tabformer: LightGBM/ })).toBeVisible();
  await expect(page.getByText("+ graph features").first()).toBeVisible();
});

test("replays are listed and a bad request is explained", async ({ page, request }) => {
  await page.goto("/replay");
  await expect(page.getByRole("heading", { name: "Recent replays" })).toBeVisible();
  const res = await request.post("/api/replays", { data: { profile: "", speed: -1 } });
  expect(res.status()).toBe(400);
});

// Runs against any assistant model; with no API key a scripted stub answers.
test("the assistant answers about an alert, shows its steps, and can rewind and start over", async ({ page }) => {
  await page.goto("/alerts?sort=score");
  await page.locator("table.t tbody tr").first().getByRole("link").click();
  await expect(page).toHaveURL(/\/alerts\/[^/?]+$/);
  await page.getByRole("button", { name: "Ask the assistant" }).click();
  const panel = page.getByRole("complementary", { name: "Assistant" });
  await expect(panel.getByText(/^About alert #/)).toBeVisible();

  await panel.getByRole("button", { name: "Has this card been flagged before?" }).click();
  await expect(panel.getByText(/How I worked this out · \d+ lookups?/)).toBeVisible({ timeout: 60_000 });
  await expect(panel.locator(".turn")).toHaveCount(1);
  await expect(panel.locator(".answer")).not.toBeEmpty();

  // Edit and resend replaces the question and its answer.
  await panel.locator(".turn .q").hover();
  await panel.getByRole("button", { name: "Edit and resend" }).click();
  await panel.getByLabel("Edit your question").fill("Is this merchant seeing other fraud?");
  await panel.getByRole("button", { name: "Send", exact: true }).first().click();
  await expect(panel.locator(".turn .q p")).toHaveText(["Is this merchant seeing other fraud?"], { timeout: 60_000 });
  await expect(panel.getByText(/How I worked this out/)).toBeVisible({ timeout: 60_000 });

  // A new chat starts empty; the earlier one is listed and reopens.
  await panel.getByRole("button", { name: "New chat" }).click();
  await expect(panel.getByRole("button", { name: "Who else is connected to this card?" })).toBeVisible();
  await panel.getByRole("button", { name: "Earlier conversations" }).click();
  await panel.getByRole("button", { name: /Is this merchant seeing other fraud\?/ }).click();
  await expect(panel.locator(".turn")).toHaveCount(1);
});
