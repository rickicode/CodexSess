function openSystemLogDetailState(entry) {
  return { showSystemLogDetail: true, systemLogDetail: entry }
}

function closeSystemLogDetailState() {
  return { showSystemLogDetail: false, systemLogDetail: null }
}

function openLogDetailState(entry) {
  return { showLogDetailModal: true, logDetailEntry: entry }
}

function closeLogDetailState() {
  return { showLogDetailModal: false, logDetailEntry: null }
}

export {
  closeLogDetailState,
  closeSystemLogDetailState,
  openLogDetailState,
  openSystemLogDetailState
}
