<script>
  export let skillSearchQuery = '';
  export let loadingSkills = false;
  export let skills = [];
  export let onClose = () => {};
  export let onSearch = () => {};
  export let onInsert = () => {};
</script>

<div class="modal-backdrop modal-backdrop-coding" role="presentation">
  <div class="modal-card modal-card-coding" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && onClose()}>
    <div class="modal-head">
      <div>
        <h3>Insert Skill</h3>
        <p class="modal-subtitle">Select available skill and insert `$skill_name` into composer.</p>
      </div>
      <button class="btn btn-secondary btn-small" onclick={onClose}>Close</button>
    </div>
    <div class="modal-body">
      <label for="skillSearchInput">Search Skill</label>
      <input
        id="skillSearchInput"
        value={skillSearchQuery}
        placeholder="Search skill name..."
        oninput={(event) => onSearch(event.currentTarget.value)}
      />
      {#if loadingSkills}
        <p class="setting-title">Loading skills...</p>
      {:else if skills.length === 0}
        <p class="setting-title">No skills found.</p>
      {:else}
        <div class="skill-list">
          {#each skills as skill}
            <button class="skill-item" onclick={() => onInsert(skill)}>
              <span>{skill}</span>
              <code>${skill}</code>
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>
