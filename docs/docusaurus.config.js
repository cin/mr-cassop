const isCI = !!process.env.CI;
module.exports = {
  title: 'mr-cassop',
  githubHost: 'github.com',
  url: isCI ? 'https://cin.github.io' : 'http://localhost:3000',
  baseUrl: isCI ? '/mr-cassop/' : '/',
  onBrokenLinks: 'throw',
  favicon: 'images/favicon.png',
  organizationName: 'cin',
  projectName: 'mr-cassop',
  themeConfig: {
    docs: {
      sidebar: {
        hideable: true,
      },
    },
    colorMode: {
      defaultMode: 'dark',
    },
    navbar: {
      hideOnScroll: false,
      title: 'mr-cassop',
      logo: {
        src: 'images/logo.svg',
        srcDark: 'images/logo.svg',
      },
      items: [
        {
          href: 'https://github.com/cin/mr-cassop',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    prism: {
      defaultLanguage: 'go',
      additionalLanguages: ['go'],
    },
    footer: {
      style: 'dark',
      links: [],
      copyright: `mr-cassop Documentation. Built with Docusaurus.`,
    },
  },
  presets: [
    [
      '@docusaurus/preset-classic',
      {
        docs: {
          path: 'docs',
          routeBasePath: '/',
          sidebarPath: require.resolve('./sidebars.js'),
          showLastUpdateTime: true,
          remarkPlugins: [
            [require('remark-toc').default, { tight: true }],
          ],
        },
        theme: {
          customCss: [require.resolve('./theme-custom.css')],
        },
      },
    ],
  ],
  plugins: [
    [require.resolve('@docusaurus/plugin-client-redirects'), { fromExtensions: ['html', 'md'] }],
  ],
};
