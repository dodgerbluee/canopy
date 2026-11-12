# UI30 contract clarification — automatic covers

User authorization: “main photo to refresh as we add more photos (if its a better photo).”

AttachPhotoInput.isCover=true remains an explicit choice for that attachment. false now lets the server consider the processed photo automatically, rather than forbidding it from becoming the cover. Existing upload clients need no new API fields. A later, better and sufficiently current whole-plant image can replace an earlier cover; Make cover still chooses a specific photo immediately. Capture dates and photo identity remain unchanged. The decision and attachment commit together; the existing single-cover invariant is retained. This supersedes the earlier Rounds comment that existing covers never change.

Original/untrusted-image decoding remains in the sandbox. A bounded local worker scores only already-normalized JPEG thumbnails; the API reads stored scores and does not decode images or invoke an LLM. Existing photos receive derived scores in bounded background batches, without changing their covers retroactively. Unscorable candidates never displace a scored cover. No health claims are inferred from the score.
