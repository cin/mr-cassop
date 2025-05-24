#!/bin/bash

echo "🔍 Checking mr-cassop documentation..."
echo "📝 Note: Documentation has been consolidated to reduce duplication"

# Check main README links
echo "📋 Main README.md:"
if [ -f "docs/docs/quickstart.md" ]; then
    echo "  ✅ quickstart.md exists"
else
    echo "  ❌ quickstart.md missing"
fi

if [ -f "docs/docs/development.md" ]; then
    echo "  ✅ development.md exists"
else
    echo "  ❌ development.md missing"
fi

if [ -f "DOCKER_BUILD.md" ]; then
    echo "  ✅ DOCKER_BUILD.md exists"
else
    echo "  ❌ DOCKER_BUILD.md missing"
fi

if [ -f "CONTRIBUTING.md" ]; then
    echo "  ✅ CONTRIBUTING.md exists"
else
    echo "  ❌ CONTRIBUTING.md missing"
fi

# Check docs structure
echo ""
echo "📂 Documentation structure:"
echo "  📁 docs/docs/ ($(ls docs/docs/*.md | wc -l | tr -d ' ') files)"
echo "  📁 docs/docs/security/ ($(ls docs/docs/security/*.md 2>/dev/null | wc -l | tr -d ' ') files)"

# Check key files
echo ""
echo "🔑 Key documentation files:"
key_files=(
    "docs/docs/home.md"
    "docs/docs/architecture-overview.md" 
    "docs/docs/cassandracluster-configuration.md"
    "docs/docs/backup-restore.md"
    "docs/docs/multi-region-cluster-configuration.md"
)

for file in "${key_files[@]}"; do
    if [ -f "$file" ]; then
        echo "  ✅ $file"
    else
        echo "  ❌ $file missing"
    fi
done

# Check build scripts
echo ""
echo "🔧 Build scripts:"
build_scripts=(
    "build-local.sh"
    "build-images.sh"
    "dev-env.sh"
    "local-values.yaml"
)

for script in "${build_scripts[@]}"; do
    if [ -f "$script" ]; then
        echo "  ✅ $script"
    else
        echo "  ❌ $script missing"  
    fi
done

echo ""
echo "🎉 Documentation check complete!" 