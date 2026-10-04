// M2 changes control placement, not the M1 transaction assertions. These helpers
// exercise the visible menus and controls instead of bypassing the UI.
async function trackAction(page, row, label) {
  const dialog = page.locator(".context-menu");
  const direct = row.getByRole("button", { name: label, exact: true });
  if ((await direct.count()) && (await direct.isVisible())) {
    await direct.click();
    return;
  }
  await row.getByRole("button", { name: /^More actions for / }).click();
  await dialog.getByRole("menuitem", { name: label, exact: true }).click();
}
async function addToPlaylist(page, row, name) {
  await trackAction(page, row, "Add to playlist");
  await page.getByRole("button", { name: /^Choose playlist:/ }).click();
  await page.getByRole("menuitemradio", { name, exact: true }).click();
  await page.getByRole("button", { name: "Add track", exact: true }).click();
  await page.locator("#action-dialog").waitFor({ state: "hidden" });
}
async function renamePlaylist(page, name) {
  await page.getByRole("button", { name: "Rename", exact: true }).click();
  await page
    .locator("#action-dialog")
    .getByLabel("Playlist name", { exact: true })
    .fill(name);
  await page.getByRole("button", { name: "Save name", exact: true }).click();
}
module.exports = { trackAction, addToPlaylist, renamePlaylist };
