#!/bin/sh
# Copies the app icons, Apple TV layered icon, Top Shelf images and Android launcher/TV banner
# from the Portico brand set (portico-internal "Brand Assets", rendered by its tools/generate.py).
# Nothing is redrawn or recoloured here. The launch assets come from scripts/make-boot-assets.py.
# Usage: sh apps/native/scripts/install-brand-icons.sh "<path to Brand Assets>"
set -e
HERE="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
SET="${1:?path to the Brand Assets folder}"
PNG="$SET/png"
INFO='"info" : {"author" : "xcode", "version" : 1}'

# --- Android: adaptive icon (glow background, mark foreground, themed monochrome), pre-adaptive
# icons, and the 320x180dp television banner (Android TV and Fire TV home screens).
RES="$HERE/android/app/src/main/res"
for D in mdpi hdpi xhdpi xxhdpi xxxhdpi; do
  mkdir -p "$RES/mipmap-$D"
  for F in ic_launcher ic_launcher_round ic_launcher_foreground ic_launcher_background ic_launcher_monochrome; do
    cp "$PNG/android/mipmap-$D/$F.png" "$RES/mipmap-$D/$F.png"
  done
done
cp "$PNG/android/portico-androidtv-banner-640x360.png" "$RES/drawable-xhdpi/tv_banner.png"

# --- iOS / iPadOS: one opaque 1024 icon (the system masks it), plus the dark and tinted appearances.
IOS="$HERE/ios/Portico/Images.xcassets/AppIcon.appiconset"
cp "$PNG/ios/portico-ios-appicon-1024.png" "$IOS/AppIcon-1024.png"
cp "$PNG/ios/portico-ios-appicon-dark-foreground-1024.png" "$IOS/AppIcon-1024-dark.png"
cp "$PNG/ios/portico-ios-appicon-tinted-1024.png" "$IOS/AppIcon-1024-tinted.png"
printf '%s\n' "{\"images\" : [{\"filename\" : \"AppIcon-1024.png\", \"idiom\" : \"universal\", \"platform\" : \"ios\", \"size\" : \"1024x1024\"}, {\"appearances\" : [{\"appearance\" : \"luminosity\", \"value\" : \"dark\"}], \"filename\" : \"AppIcon-1024-dark.png\", \"idiom\" : \"universal\", \"platform\" : \"ios\", \"size\" : \"1024x1024\"}, {\"appearances\" : [{\"appearance\" : \"luminosity\", \"value\" : \"tinted\"}], \"filename\" : \"AppIcon-1024-tinted.png\", \"idiom\" : \"universal\", \"platform\" : \"ios\", \"size\" : \"1024x1024\"}], $INFO}" > "$IOS/Contents.json"

# --- tvOS: layered icon (glow behind, wordmark in front, so it lifts on focus) and Top Shelf.
A="$HERE/tvos/Portico/Images.xcassets/App Icon & Top Shelf Image.brandassets"
L="$PNG/appletv/layers"
S="$A/App Icon.imagestack"
cp "$L/portico-tv-layer-back-400x240.png" "$S/Back.imagestacklayer/Content.imageset/Back.png"
cp "$L/portico-tv-layer-back-800x480.png" "$S/Back.imagestacklayer/Content.imageset/Back@2x.png"
cp "$L/portico-tv-layer-front-wordmark-400x240.png" "$S/Front.imagestacklayer/Content.imageset/Front.png"
cp "$L/portico-tv-layer-front-wordmark-800x480.png" "$S/Front.imagestacklayer/Content.imageset/Front@2x.png"
S="$A/App Icon - App Store.imagestack"
cp "$L/portico-tv-layer-back-1280x768.png" "$S/Back.imagestacklayer/Content.imageset/Back.png"
cp "$L/portico-tv-layer-front-wordmark-1280x768.png" "$S/Front.imagestacklayer/Content.imageset/Front.png"
cp "$PNG/appletv/portico-tv-topshelf-1920x720.png" "$A/Top Shelf Image.imageset/shelf.png"
cp "$PNG/appletv/portico-tv-topshelf-3840x1440.png" "$A/Top Shelf Image.imageset/shelf@2x.png"
cp "$PNG/appletv/portico-tv-topshelf-wide-2320x720.png" "$A/Top Shelf Image Wide.imageset/shelf.png"
cp "$PNG/appletv/portico-tv-topshelf-wide-4640x1440.png" "$A/Top Shelf Image Wide.imageset/shelf@2x.png"
echo "icons installed"
