package mediaexec

// LocalInputFormats is the closed demuxer set for a single catalogued media
// file. It includes the prepared-media audio formats and the video containers
// accepted by assets.SupportedPath. Playlist, concat, DASH and image-sequence
// demuxers are deliberately absent: a file protocol whitelist alone still lets
// those demuxers open a second local file.
// Disc readers select their own explicit demuxer in the admitted input options.
const LocalInputFormats = "mov,matroska,mp3,flac,ogg,wav,aac,mpegts,avi,aiff,asf,ape,wv,tta,amr,w64,caf,dsf,iff,mpc,mpc8,mpeg,mpegvideo,flv,mxf,dv,rm,yuv4mpegpipe,nut,nsv,flic,bink,smk,mjpeg,h264,hevc,vvc"
