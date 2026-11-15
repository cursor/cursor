const viewerEl = document.getElementById("viewer");
const actionsEl = document.getElementById("actions");
const accessNoteEl = document.getElementById("access-note");

function setCtasEnabled(enabled) {
  if (!actionsEl) return;
  for (const link of actionsEl.querySelectorAll("a.cta")) {
    if (enabled) {
      link.removeAttribute("aria-disabled");
      link.classList.remove("is-disabled");
      link.tabIndex = 0;
    } else {
      link.setAttribute("aria-disabled", "true");
      link.classList.add("is-disabled");
      link.tabIndex = -1;
    }
  }
}

function setAccessNote(text) {
  if (!accessNoteEl) return;
  if (!text) {
    accessNoteEl.hidden = true;
    accessNoteEl.textContent = "";
    return;
  }
  accessNoteEl.hidden = false;
  accessNoteEl.textContent = text;
}

async function loadViewer() {
  setCtasEnabled(false);

  try {
    const response = await fetch("/api/viewer", {
      credentials: "same-origin",
      headers: { accept: "application/json" },
    });
    const data = await response.json();

    if (!response.ok || !data.verified) {
      viewerEl.dataset.state = "unverified";
      viewerEl.textContent = "Sign in required for private access";
      setAccessNote(
        "This surface needs a verified Cursor viewer. Open it while signed in to Cursor.",
      );
      return;
    }

    const name = data.name || "Viewer";
    if (data.kind === "key") {
      viewerEl.dataset.state = "verified";
      viewerEl.textContent = name;
      setAccessNote("Calling as an API key (not a person).");
    } else if (data.simulated) {
      viewerEl.dataset.state = "simulated";
      viewerEl.textContent = name;
      setAccessNote(
        "Local preview uses a simulated viewer — not a real sign-in. Publish private for a gated viewer.",
      );
    } else {
      viewerEl.dataset.state = "verified";
      viewerEl.textContent = name;
      setAccessNote("");
    }

    setCtasEnabled(true);
  } catch {
    viewerEl.dataset.state = "unverified";
    viewerEl.textContent = "Could not verify viewer";
    setAccessNote("Viewer check failed. Retry while signed in to Cursor.");
  }
}

if (actionsEl) {
  actionsEl.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const link = target.closest("a.cta");
    if (link?.classList.contains("is-disabled")) {
      event.preventDefault();
    }
  });
}

loadViewer();
