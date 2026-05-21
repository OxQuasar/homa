<script lang="ts">
  import MysteryPage from '$lib/MysteryPage.svelte';
</script>

<MysteryPage
  title="Greek Ἑλληνικά"
  backHref="/mysteries/ancient-academy"
>
  <header>
    <h1>Greek <span class="accent">Ἑλληνικά</span></h1>
  </header>

  <ul class="items">
    <li>
      <a href="/library/poimandres">
        <h2>Poimandres <span class="accent">Ποιμάνδρης</span></h2>
        <p class="note">Corpus Hermeticum I</p>
      </a>
    </li>
  </ul>
</MysteryPage>

<style>
  /* The inline Greek accent inside the h1, mirroring how 易經 is
     styled on the I Ching page. */
  :global(.mp-content h1 .accent) {
    margin-left: 0.6rem;
    font-weight: 400;
    opacity: 0.85;
  }
</style>
