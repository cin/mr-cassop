const isCI = !!process.env.CI;
module.exports = {
  title: 'Cassandra Operator',
  githubHost: 'github.ibm.com',
  url: isCI ? 'https://pages.github.ibm.com' : 'http://localhost:3000',
  baseUrl: isCI ? '/TheWeatherCompany/cassandra-operator/' : '/',
  onBrokenLinks: 'throw',
  favicon: 'images/favicon.png',
  organizationName: 'TheWeatherCompany',
  projectName: 'cassandra-operator',
  themeConfig: {
    sidebarCollapsible: true,
    hideableSidebar: true,
    colorMode: {
      defaultMode: 'dark',
    },
    navbar: {
      hideOnScroll: false,
      title: 'Cassandra Operator',
      logo: {
        src: 'images/logo.svg',
        srcDark: 'images/logo.svg',
      },
      items: [
        {
          href: 'https://github.com/TheWeatherCompany/cassandra-operator',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    prism: {
      defaultLanguage: 'groovy',
      additionalLanguages: ['groovy'],
    },
  },
  presets: [
    [
      '@docusaurus/preset-classic',
      {
        docs: {
          routeBasePath: '/',
          sidebarPath: require.resolve('./sidebars.js'),
          showLastUpdateTime: true,
          remarkPlugins: [
            [require('remark-toc'), { tight: true }],
          ],
        },
        theme: {
          customCss: require.resolve('./theme-custom.css'),
        },
      },
    ],
  ],
  plugins: [
    [require.resolve('docusaurus-lunr-search'), { languages: ['en'], indexBaseUrl: true }],
    [require.resolve('@docusaurus/plugin-client-redirects'), { fromExtensions: ['html', 'md'] }],
  ],
};
