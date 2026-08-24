<template>
  <!-- One place that decides the admin section's two-column shape, so the nav
       cannot end up a different width or a different distance from the content
       on one page. Wider than the pages were on their own: the nav takes a
       column out of the line length, and this puts it back.

       Column only from lg. Below it the nav is a scrolling row above the
       content, because ten stacked entries would push every admin page a
       screen and a half down on a phone. -->
  <div class="mx-auto max-w-7xl px-4 py-12 sm:px-6">
    <div class="flex flex-col gap-6 lg:flex-row lg:gap-8">
      <div class="lg:w-56 lg:shrink-0">
        <AdminNav />
      </div>
      <!-- min-w-0 so a wide table scrolls inside this column instead of
           stretching it and pushing the nav off screen.

           The first-child margin reset is for the `mt-8` every admin page opens
           with: it was the gap under the old top bar, and beside a column it
           only drops the heading out of line with the top of the nav. Done here
           rather than in fifteen pages. -->
      <div class="min-w-0 flex-1 [&>*:first-child]:mt-0">
        <slot />
      </div>
    </div>
  </div>
</template>
