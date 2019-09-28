/**
 * Copyright 2017 Google Inc. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
'use strict';

const {Storage} = require('@google-cloud/storage');
const path = require('path');
const os = require('os');
const ffmpeg = require('fluent-ffmpeg');
const ffmpeg_static = require('ffmpeg-static');

// Makes an ffmpeg command return a promise.
function promisifyCommand(command) {
  return new Promise((resolve, reject) => {
    command.on('end', resolve).on('error', reject).run();
  });
}

/**
 * When an audio is uploaded in the Storage bucket We generate a mono channel audio automatically using
 * node-fluent-ffmpeg.
 */
exports.ConvertAudio = async (object, context) => {
    const fileBucket = object.bucket; // The Storage bucket that contains the file.
    const filePath = object.name; // File path in the bucket.
    const contentType = object.contentType; // File content type.

    // Exit if this is triggered on a file that is not an audio.
    if (!contentType.startsWith('audio/')) {
        console.log('This is not an audio.');
        return null;
    }

    // Get the file name.
    const fileName = path.basename(filePath);
    // Exit if the audio is already converted.
    if (fileName.endsWith('_output.flac')) {
        console.log('Already a converted audio.');
        return null;
    }

    // Download file from bucket.
    const bucket = new Storage().bucket(fileBucket);
    // We add a '_output.flac' suffix to target audio file name. That's where we'll upload the converted audio.
    const targetTempFileName = fileName.replace(/\.[^/.]+$/, '') + '_output.flac';
    const targetStorageFilePath = path.join(path.dirname(filePath), targetTempFileName);

    console.log('Streaming audio from', filePath);
    // Convert the audio to mono channel using FFMPEG.

    let readStream = bucket.file(filePath).createReadStream();
    console.log("Created read stream");
    let writeStream = bucket.file(targetStorageFilePath).createWriteStream();
    console.log("Created write stream");

    let command = ffmpeg(readStream)
        .setFfmpegPath(ffmpeg_static.path)
        .audioChannels(1)
        .audioFrequency(16000)
        .format('flac')
        .output(writeStream);

    await promisifyCommand(command);

    console.log('Output audio uploaded to', targetStorageFilePath);

    return null;
};
