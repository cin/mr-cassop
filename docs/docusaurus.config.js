const isCI = !!process.env.CI;
module.exports = {
  title: 'mr-cassop',
  githubHost: 'github.com',
  url: isCI ? 'https://mr-cassop-docs.dev.sun.weather.com' : 'http://localhost:3000',
  baseUrl: '/',
  onBrokenLinks: 'throw',
  favicon: 'images/favicon.png',
  organizationName: 'TheWeatherCompany',
  projectName: 'mr-cassop',
  themeConfig: {
    hideableSidebar: true,
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
          href: 'https://mr-cassop-docs.dev.sun.weather.com',
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
            [require('remark-toc'), { tight: true }],
          ],
        },
        theme: {
          customCss: [require.resolve('./theme-custom.css')],
        },
      },
    ],
  ],
  plugins: [
    [require.resolve('docusaurus-lunr-search'), { languages: ['en'], indexBaseUrl: true }],
    [require.resolve('@docusaurus/plugin-client-redirects'), { fromExtensions: ['html', 'md'] }],
  ],
};
